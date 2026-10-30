package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ottermq/jaildeck/internal/vms"
)

// The seed's files, in the order the view shows them, as vm-bhyve writes them
// into <guest>/.cloud-init (vcf_dir_u). seed.iso, built from them at the first
// start, is binary and left out.
var seedFileNames = []string{"meta-data", "user-data", "network-config"}

// VMFiles reads a guest's configuration and its seed's files off the default
// datastore, each at most limit bytes (#127, JD-048).
//
// Plain file I/O, as VMApplied's reads are. Everything here is root:wheel; the
// configuration and the seed are 0644, and a user-data given with -u keeps its
// own mode, 0600 for Jail Deck's -- measured (vcf_dir_u). The service runs as
// root and reads them all, and masks what must not be shown.
func (a *Adapter) VMFiles(ctx context.Context, name string, limit int) ([]vms.GuestFile, error) {
	dir, _, err := a.vmDatastore(ctx)
	if err != nil {
		return nil, err
	}
	conf, err := readCapped(vmConfPath(dir, name), limit)
	if err != nil {
		return nil, err
	}
	if !conf.Present {
		return nil, vms.ErrNoSuchVM
	}
	conf.Name = name + ".conf"
	files := []vms.GuestFile{conf}
	for _, seed := range seedFileNames {
		file, err := readCapped(filepath.Join(dir, name, ".cloud-init", seed), limit)
		if err != nil {
			return nil, err
		}
		file.Name = seed
		files = append(files, file)
	}
	return files, nil
}

// readCapped reads at most limit bytes of path, and says whether there was
// more. A file that is not there is Present false, not an error.
func readCapped(path string, limit int) (vms.GuestFile, error) {
	file := vms.GuestFile{Path: path}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return file, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return file, fmt.Errorf("reading %s: %w", path, err)
	}
	file.Present = true
	if len(content) > limit {
		content, file.Truncated = content[:limit], true
	}
	file.Content = string(content)
	return file, nil
}
