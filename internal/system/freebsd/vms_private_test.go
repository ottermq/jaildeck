package freebsd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

// vm create leaves a guest's directory 0755 (vcf_dir_u's "."); made private,
// it is 0700, which vm-bhyve keeps across starts (vpd_mode_after_restarts).
func TestVMMakePrivateLeavesTheDirectoryToRoot(t *testing.T) {
	store := t.TempDir()
	guest := filepath.Join(store, "web")
	if err := os.MkdirAll(guest, 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.String() == "vm datastore list" {
			return system.CommandResult{Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
				"default         zfs         " + store + "                 tank/vm\n"}, nil
		}
		t.Fatalf("unexpected command %q", cmd.String())
		return system.CommandResult{}, nil
	}))
	command, err := a.VMMakePrivate(context.Background(), "web")
	if err != nil || command != "chmod 0700 "+guest {
		t.Fatalf("%q, %v", command, err)
	}
	info, err := os.Stat(guest)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, %v", info.Mode().Perm(), err)
	}
	if _, err := a.VMMakePrivate(context.Background(), "gone"); err == nil {
		t.Error("a guest with no directory was made private")
	}
}
