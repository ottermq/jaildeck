package freebsd

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

func writeAdapter(t *testing.T) *Adapter {
	t.Helper()
	adapter := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		t.Error("WriteConfig ran a command; it writes a file")
		return system.CommandResult{}, nil
	}))
	adapter.confDir = filepath.Join(t.TempDir(), "jail.conf.d")
	adapter.confFile = filepath.Join(t.TempDir(), "missing.conf")
	return adapter
}

func TestWriteConfig(t *testing.T) {
	adapter := writeAdapter(t)

	path, err := adapter.WriteConfig(context.Background(), "db1", "db1 {\n}\n")
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if want := filepath.Join(adapter.confDir, "db1.conf"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back what was written: %v", err)
	}
	if string(content) != "db1 {\n}\n" {
		t.Errorf("content = %q, want the stanza it was handed", content)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The same mode as the files already in /etc/jail.conf.d on a host.
	if got := info.Mode().Perm(); got != fs.FileMode(confFileMode) {
		t.Errorf("mode = %04o, want %04o", got, confFileMode)
	}
}

// The directory does not exist on a host that has never had a jail.
func TestWriteConfigCreatesTheDirectory(t *testing.T) {
	adapter := writeAdapter(t)
	if _, err := os.Stat(adapter.confDir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the test's conf dir already exists: %v", err)
	}
	if _, err := adapter.WriteConfig(context.Background(), "db1", "db1 {\n}\n"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if _, err := os.Stat(adapter.confDir); err != nil {
		t.Errorf("conf dir was not created: %v", err)
	}
}

// A configuration Jail Deck did not just write belongs to whoever wrote it,
// and may well be a jail that is running.
func TestWriteConfigWillNotOverwrite(t *testing.T) {
	adapter := writeAdapter(t)
	if _, err := adapter.WriteConfig(context.Background(), "db1", "first\n"); err != nil {
		t.Fatalf("first WriteConfig: %v", err)
	}

	path, err := adapter.WriteConfig(context.Background(), "db1", "second\n")
	if err == nil {
		t.Fatal("second WriteConfig = nil error, want a refusal")
	}
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusConflict {
		t.Fatalf("error = %v, want 409", err)
	}

	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading the file back: %v", readErr)
	}
	if string(content) != "first\n" {
		t.Errorf("content = %q, want the first write untouched", content)
	}
}

// The round trip that matters: a stanza this project generates has to be one
// this project can read back, or the jail is written to disk and then missing
// from every listing (JD-004).
func TestWriteConfigRoundTripsThroughTheParser(t *testing.T) {
	adapter := writeAdapter(t)

	spec := jails.Spec{
		Name:     "db1",
		Hostname: "db1",
		Template: "tank/jails/templates/15.1-RELEASE@base",
		Network:  jails.Network{Mode: jails.NetworkAlias, IP4: "192.0.2.10", Interface: "em0"},
	}
	content, err := spec.Stanza("/usr/local/jails/containers/db1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := adapter.WriteConfig(context.Background(), "db1", content); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	found, err := adapter.listConfiguredJails()
	if err != nil {
		t.Fatalf("listConfiguredJails: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("parsed %d jails out of the written stanza, want 1: %+v", len(found), found)
	}
	got := found[0]
	if got.Name != "db1" {
		t.Errorf("name = %q, want db1", got.Name)
	}
	if got.Hostname != "db1" {
		t.Errorf("hostname = %q, want db1", got.Hostname)
	}
	if got.Path != "/usr/local/jails/containers/db1" {
		t.Errorf("path = %q, want the jail's root mountpoint", got.Path)
	}
	if got.IP != "192.0.2.10" {
		t.Errorf("ip = %q, want the alias the spec asked for", got.IP)
	}
	if got.Status != jails.JailStatusStopped {
		t.Errorf("status = %q, want stopped: a configured jail that is not running", got.Status)
	}
}

func TestSetAutostart(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    []string
	}{
		{name: "enable", enabled: true, want: []string{"jail_list+=db1"}},
		{name: "disable", enabled: false, want: []string{"jail_list-=db1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got system.Command
			adapter := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
				got = cmd
				return system.CommandResult{}, nil
			}))

			command, err := adapter.SetAutostart(context.Background(), "db1", tt.enabled)
			if err != nil {
				t.Fatalf("SetAutostart: %v", err)
			}
			if got.Name != "sysrc" {
				t.Errorf("command = %q, want sysrc", got.Name)
			}
			if len(got.Args) != len(tt.want) || got.Args[0] != tt.want[0] {
				t.Errorf("args = %v, want %v", got.Args, tt.want)
			}
			// What comes back is what the audit log records, so it has to be
			// the command that ran.
			if !strings.Contains(command, tt.want[0]) {
				t.Errorf("reported command = %q, want it to contain %q", command, tt.want[0])
			}
			if got.Timeout == 0 {
				t.Error("no timeout: every command runs under one (JD-014)")
			}
		})
	}
}

func TestSetAutostartReportsTheCommandItRanWhenItFails(t *testing.T) {
	wantErr := errors.New("sysrc: /etc/rc.conf: Permission denied")
	adapter := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{ExitCode: 1}, wantErr
	}))

	command, err := adapter.SetAutostart(context.Background(), "db1", true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want the runner's", err)
	}
	// The audit entry is written from this even on failure, so it must not be
	// empty just because the command did not work.
	if !strings.Contains(command, "jail_list+=db1") {
		t.Errorf("reported command = %q, want the command that was attempted", command)
	}
}
