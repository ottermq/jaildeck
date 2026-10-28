package freebsd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ottermq/jaildeck/internal/console"
)

// The #153 incident on isengard: a vm console from a shell (cu, two
// processes) and Jail Deck itself (pid 21543) on the same console device.
func TestParseHoldersLeavesJailDeckOut(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "hostfacts", "isengard-induced", "con_fstat_shared.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseHolders(string(out), 21543)
	want := []console.Holder{{Command: "cu", PID: 38812}, {Command: "cu", PID: 38813}}
	if !slices.Equal(got, want) {
		t.Errorf("holders = %+v, want %+v", got, want)
	}
	if got := parseHolders("USER     CMD          PID   FD MOUNT      INUM MODE         SZ|DV R/W NAME\n", 1); len(got) != 0 {
		t.Errorf("no rows: %+v", got)
	}
}
