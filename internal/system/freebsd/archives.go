package freebsd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ottermq/jaildeck/internal/housekeeping"
	"github.com/ottermq/jaildeck/internal/system"
)

// ArchiveAdapter lists the archives Jail Deck downloaded and keeps: a
// release's base.txz in media/<release>/ (JD-006), and an image source's
// archive in <datastore>/.images (JD-037). Only Jail Deck's own paths: a
// file an operator put beside them is theirs and not a guess this makes
// (#125). Housekeeping's ArchiveSource.
type ArchiveAdapter struct {
	*Adapter
	// media is overridden in tests; a host never changes it.
	media string
}

func NewArchiveAdapter(runner system.CommandRunner) *ArchiveAdapter {
	return &ArchiveAdapter{Adapter: NewAdapter(runner), media: mediaDir}
}

func (a *ArchiveAdapter) Archives(ctx context.Context) ([]housekeeping.Archive, error) {
	out := []housekeeping.Archive{}
	releases, err := filepath.Glob(filepath.Join(a.media, "*", "base.txz"))
	if err != nil {
		return nil, err
	}
	for _, path := range releases {
		if archive, ok, err := stat(path, filepath.Base(filepath.Dir(path))); err != nil {
			return out, err
		} else if ok {
			out = append(out, archive)
		}
	}

	// No vm-bhyve, or none configured, is no image archives: an answer.
	dir, _, err := a.vmDatastore(ctx)
	if err != nil {
		return out, nil
	}
	for _, entry := range imageCatalog {
		archive, ok, err := stat(filepath.Join(dir, ".images", entry.archive), "the "+entry.Name+" image")
		if err != nil {
			return out, err
		}
		if ok {
			out = append(out, archive)
		}
	}
	return out, nil
}

func stat(path, what string) (housekeeping.Archive, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return housekeeping.Archive{}, false, nil
	}
	if err != nil {
		return housekeeping.Archive{}, false, err
	}
	if !info.Mode().IsRegular() {
		return housekeeping.Archive{}, false, nil
	}
	return housekeeping.Archive{Path: path, Size: info.Size(), Modified: info.ModTime(), For: what}, true, nil
}
