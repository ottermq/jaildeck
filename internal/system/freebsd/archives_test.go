package freebsd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

// archiveHost is a media directory and a datastore at temporary directories:
// a release's archive in Jail Deck's layout, one an operator left in the old
// one, an extracted release with no archive, and the Debian image's archive.
func archiveHost(t *testing.T, datastore bool) *ArchiveAdapter {
	t.Helper()
	media, store := t.TempDir(), t.TempDir()
	files := map[string]string{
		filepath.Join(media, "15.1-RELEASE", "base.txz"):           "fifteen",
		filepath.Join(media, "15.1-RELEASE-base.txz"):              "the operator's",
		filepath.Join(media, "14.3-RELEASE", "MANIFEST"):           "no archive",
		filepath.Join(store, ".images", imageCatalog[0].archive):   "debian",
		filepath.Join(store, ".images", "someone-elses-image.raw"): "not ours",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := NewArchiveAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.String() != "vm datastore list" {
			t.Fatalf("unexpected command %q", cmd.String())
		}
		if !datastore {
			return system.CommandResult{}, errors.New("vm: not found")
		}
		return system.CommandResult{Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
			"default         zfs         " + store + "                 tank/vm\n"}, nil
	}))
	a.media = media
	return a
}

func TestArchivesAreJailDecksOwnOnly(t *testing.T) {
	archives, err := archiveHost(t, true).Archives(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 2 {
		t.Fatalf("archives = %+v", archives)
	}
	if archives[0].For != "15.1-RELEASE" || archives[0].Size != int64(len("fifteen")) {
		t.Errorf("release archive = %+v", archives[0])
	}
	if archives[1].For != "the "+imageCatalog[0].Name+" image" || archives[1].Size != int64(len("debian")) {
		t.Errorf("image archive = %+v", archives[1])
	}
}

func TestNoVMBhyveIsNoImageArchives(t *testing.T) {
	archives, err := archiveHost(t, false).Archives(context.Background())
	if err != nil || len(archives) != 1 || archives[0].For != "15.1-RELEASE" {
		t.Errorf("archives = %+v, %v", archives, err)
	}
}
