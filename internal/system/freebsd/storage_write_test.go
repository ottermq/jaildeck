package freebsd

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

// recordingRunner accepts every command and remembers the argument array.
//
// These are writes, so there are no captures: capture.sh is read-only and must
// stay that way. What is asserted instead is the argv itself, which is the
// thing that reaches the host -- CommandRunner runs no shell, so the array is
// the command, and getting it right is the whole job of this file.
type recordingRunner struct {
	ran    []system.Command
	stdout string
	err    error
}

func (r *recordingRunner) Run(_ context.Context, cmd system.Command) (system.CommandResult, error) {
	r.ran = append(r.ran, cmd)
	if r.err != nil {
		return system.CommandResult{ExitCode: 1, Stderr: r.err.Error()}, r.err
	}
	return system.CommandResult{Stdout: r.stdout}, nil
}

func TestStorageWriteCommands(t *testing.T) {
	tests := []struct {
		name string
		call func(*StorageAdapter) error
		want []string
	}{
		{
			name: "create with no properties",
			call: func(a *StorageAdapter) error {
				_, err := a.CreateDataset(context.Background(), "tank/jails/containers/ottermq/data", nil)
				return err
			},
			want: []string{"create", "tank/jails/containers/ottermq/data"},
		},
		{
			// Name order, not map order: Go randomises map iteration, so
			// without sorting the same request would produce a different
			// command each run and the audit log could not be compared.
			name: "create with properties, in name order",
			call: func(a *StorageAdapter) error {
				_, err := a.CreateDataset(context.Background(), "tank/jails/containers/ottermq/data", map[string]string{
					"refquota":    "10G",
					"atime":       "off",
					"mountpoint":  "/usr/local/jails/containers/ottermq/data",
					"recordsize":  "16K",
					"compression": "lz4",
				})
				return err
			},
			want: []string{
				"create",
				"-o", "atime=off",
				"-o", "compression=lz4",
				"-o", "mountpoint=/usr/local/jails/containers/ottermq/data",
				"-o", "recordsize=16K",
				"-o", "refquota=10G",
				"tank/jails/containers/ottermq/data",
			},
		},
		{
			// zfs set takes them bare, with no -o, and any number of them, so
			// three tags are one command and one audit line.
			name: "set takes bare property=value",
			call: func(a *StorageAdapter) error {
				_, err := a.SetProperties(context.Background(), "tank/jails/containers/ottermq", map[string]string{
					"otter:tier":     "broker",
					"otter:instance": "mq-01",
				})
				return err
			},
			want: []string{"set", "otter:instance=mq-01", "otter:tier=broker", "tank/jails/containers/ottermq"},
		},
		{
			// A value may contain anything, including an equals sign: zfs
			// splits on the first one, and no shell is involved to re-split it.
			name: "a value containing an equals sign stays one argument",
			call: func(a *StorageAdapter) error {
				_, err := a.SetProperties(context.Background(), "tank/jails", map[string]string{
					"otter:selector": "role=db,env=prod",
				})
				return err
			},
			want: []string{"set", "otter:selector=role=db,env=prod", "tank/jails"},
		},
		{
			name: "inherit takes one property",
			call: func(a *StorageAdapter) error {
				_, err := a.InheritProperty(context.Background(), "tank/jails/containers/ottermq", "otter:tier")
				return err
			},
			want: []string{"inherit", "otter:tier", "tank/jails/containers/ottermq"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingRunner{}
			if err := tt.call(&StorageAdapter{Adapter: NewAdapter(runner)}); err != nil {
				t.Fatalf("call() error = %v", err)
			}
			if len(runner.ran) != 1 {
				t.Fatalf("ran %d commands, want 1: %v", len(runner.ran), runner.ran)
			}
			if runner.ran[0].Name != "zfs" {
				t.Errorf("ran %q, want zfs", runner.ran[0].Name)
			}
			if !reflect.DeepEqual(runner.ran[0].Args, tt.want) {
				t.Errorf("args =\n  %q\nwant\n  %q", runner.ran[0].Args, tt.want)
			}
		})
	}
}

// A failed write still reports the command it tried. That line is exactly what
// an operator wants in the audit log when a create was refused.
func TestStorageWriteReportsTheCommandOnFailure(t *testing.T) {
	runner := &recordingRunner{err: errors.New("exit status 1")}
	adapter := &StorageAdapter{Adapter: NewAdapter(runner)}

	result, err := adapter.CreateDataset(context.Background(), "tank/jails/x", map[string]string{"refquota": "1G"})
	if err == nil {
		t.Fatal("CreateDataset() returned no error")
	}
	if result.Command != "zfs create -o refquota=1G tank/jails/x" {
		t.Errorf("Command = %q, want the command it tried", result.Command)
	}
	if result.Dataset != "tank/jails/x" {
		t.Errorf("Dataset = %q", result.Dataset)
	}
}

// The embedded *Adapter still answers the read side, which is what lets one
// type satisfy storage.StorageSystem while *Adapter satisfies host.HostSystem.
func TestStorageAdapterStillReads(t *testing.T) {
	runner := &replayRunner{t: t, set: "isengard", captures: withStorageRoots("tank")}
	datasets, err := NewStorageAdapter(runner).DatasetsUnder(context.Background(), []string{"tank"})
	if want := capturedLines(t, "isengard", "zfs_list_space_rp_tank"); err != nil || len(datasets) != want {
		t.Errorf("DatasetsUnder() = %d datasets, %v; want %d", len(datasets), err, want)
	}
}

// The move commands. -u on the rename is the one flag that matters here: a
// plain rename unmounts and remounts inside the same command, which on a
// running jail is the filesystem disappearing from under a live process.
func TestMoveCommands(t *testing.T) {
	tests := []struct {
		name string
		call func(*StorageAdapter) error
		want []string
	}{
		{
			name: "rename always passes -u",
			call: func(a *StorageAdapter) error {
				_, err := a.RenameDataset(context.Background(),
					"tank/jails/containers/ottermq/data", "tank/jails/retained/ottermq-data")
				return err
			},
			want: []string{"rename", "-u", "tank/jails/containers/ottermq/data", "tank/jails/retained/ottermq-data"},
		},
		{
			name: "unmount",
			call: func(a *StorageAdapter) error {
				_, err := a.Unmount(context.Background(), "tank/jails/retained/x")
				return err
			},
			want: []string{"unmount", "tank/jails/retained/x"},
		},
		{
			name: "mount",
			call: func(a *StorageAdapter) error {
				_, err := a.Mount(context.Background(), "tank/jails/retained/x")
				return err
			},
			want: []string{"mount", "tank/jails/retained/x"},
		},
		{
			name: "mounted reads one property",
			call: func(a *StorageAdapter) error {
				_, err := a.Mounted(context.Background(), "tank/jails/retained/x")
				return err
			},
			want: []string{"get", "-H", "-o", "value", "mounted", "tank/jails/retained/x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingRunner{stdout: "yes\n"}
			if err := tt.call(&StorageAdapter{Adapter: NewAdapter(runner)}); err != nil {
				t.Fatalf("call() error = %v", err)
			}
			if len(runner.ran) != 1 || !reflect.DeepEqual(runner.ran[0].Args, tt.want) {
				t.Errorf("args = %q, want %q", runner.ran, tt.want)
			}
		})
	}
}

func TestMountedReadsYesAndNo(t *testing.T) {
	for stdout, want := range map[string]bool{"yes\n": true, "no\n": false, "-\n": false} {
		runner := &recordingRunner{stdout: stdout}
		got, err := (&StorageAdapter{Adapter: NewAdapter(runner)}).Mounted(context.Background(), "tank/x")
		if err != nil || got != want {
			t.Errorf("Mounted() for %q = %v, %v; want %v", stdout, got, err, want)
		}
	}
}

// What the host says for a mounted dataset, captured rather than constructed.
// The two calls that move a dataset both fail when this is wrong, so it is
// asked before either of them runs.
func TestMountedFixture(t *testing.T) {
	probe := readFixtureLine(t, "isengard", "PROBE_DATASET")
	runner := &replayRunner{t: t, set: "isengard", captures: map[string]string{
		"zfs get -H -o value mounted " + probe: "zfs_get_mounted",
	}}
	mounted, err := NewStorageAdapter(runner).Mounted(context.Background(), probe)
	if err != nil || !mounted {
		t.Errorf("Mounted(%s) = %v, %v; want true", probe, mounted, err)
	}
}
