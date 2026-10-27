package freebsd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// imageSource is one catalog entry: where its archive is, which file vouches
// for it, and which member of the archive is the disk (JD-037).
type imageSource struct {
	images.Source
	// dir is the directory the archive and its checksum file are published
	// in.
	dir     string
	archive string
	sums    string
	disk    string
}

// imageCatalog is every image Jail Deck can prepare. Debian publishes its
// cloud images in three forms; the .tar.xz is the smallest and tar in base
// reads it, where the qcow2 would need qemu-img (JD-019, JD-037).
var imageCatalog = []imageSource{
	{
		Source: images.Source{
			Name:        "debian-13",
			Description: "Debian 13 (trixie), genericcloud, amd64, with cloud-init",
			URL:         debianTrixie + "/debian-13-genericcloud-amd64.tar.xz",
		},
		dir:     debianTrixie,
		archive: "debian-13-genericcloud-amd64.tar.xz",
		sums:    "SHA512SUMS",
		disk:    "disk.raw",
	},
}

// debianTrixie is where Debian publishes 13's current cloud images. "latest"
// moves with each build; the checksum recorded on an image names the one it
// was written from.
const debianTrixie = "https://cloud.debian.org/images/cloud/trixie/latest"

// The deadlines for an image's stages (JD-014). Measured on isengard: the
// archive in 69-79 s, its checksum in 1 s, listing it in 13 s, writing the
// disk in 18 s. Each is far beyond that, for a slower link or pool.
const (
	imageFetchTimeout = 45 * time.Minute
	imageReadTimeout  = 10 * time.Minute
	imageWriteTimeout = 30 * time.Minute
	// zvolDeviceWait is how long a new zvol's device node gets to appear.
	// vm-bhyve gives its own 30 seconds for the same thing; it was there at
	// once every time it was measured.
	zvolDeviceWait = 30 * time.Second
)

// ImageAdapter keeps VM images as zvols in vm-bhyve's datastore. It
// implements images.ImageSystem (JD-037).
type ImageAdapter struct {
	*Adapter
	// exists reports whether a path is there; overridden in tests, which
	// have no zvol devices.
	exists func(string) bool
}

func NewImageAdapter(runner system.CommandRunner) *ImageAdapter {
	return &ImageAdapter{Adapter: NewAdapter(runner), exists: pathExists}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (a *ImageAdapter) Sources() []images.Source {
	out := make([]images.Source, 0, len(imageCatalog))
	for _, entry := range imageCatalog {
		out = append(out, entry.Source)
	}
	return out
}

func lookupSource(name string) (imageSource, error) {
	for _, entry := range imageCatalog {
		if entry.Name == name {
			return entry, nil
		}
	}
	return imageSource{}, fmt.Errorf("no image source %q", name)
}

// Store finds vm-bhyve's default datastore, after the same `vm list` the VMs
// page reads: vm-bhyve says there whether it is installed and configured.
func (a *ImageAdapter) Store(ctx context.Context) (images.Store, error) {
	listing, err := a.VMList(ctx)
	if err != nil {
		return images.Store{}, err
	}
	switch listing.VMBhyve {
	case vms.VMBhyveNotInstalled:
		return images.Store{VMBhyve: images.VMBhyveNotInstalled,
			Problem: "vm-bhyve is not installed"}, nil
	case vms.VMBhyveNotConfigured:
		return images.Store{VMBhyve: images.VMBhyveNotConfigured, Problem: listing.Problem}, nil
	}
	stores, err := a.runHost(ctx, system.Command{Name: "vm", Args: []string{"datastore", "list"}, Timeout: vmTimeout})
	if err != nil {
		return images.Store{}, err
	}
	dir, dataset := defaultDatastore(stores.Stdout)
	if dataset == "" {
		return images.Store{VMBhyve: images.VMBhyveReady, Problem: fmt.Sprintf(
			"vm-bhyve's default datastore, %s, is not on ZFS; an image is a zvol, so it needs one that is",
			dir)}, nil
	}
	return images.Store{
		VMBhyve: images.VMBhyveReady,
		Dataset: dataset + "/.images",
		Path:    path.Join(dir, ".images"),
	}, nil
}

// defaultDatastore reads the default datastore's directory and ZFS dataset
// out of `vm datastore list`:
//
//	NAME            TYPE        PATH                      ZFS DATASET
//	default         zfs         /jd-vmcap                 tank/jd-vmcap
//
// The dataset is empty for a datastore that is a plain directory.
func defaultDatastore(out string) (dir, dataset string) {
	for _, line := range nonEmptyLines(out) {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "default" {
			dir = fields[2]
			if len(fields) >= 4 && fields[3] != "-" {
				dataset = fields[3]
			}
		}
	}
	return dir, dataset
}

// imageListColumns is what Images asks zfs for, in order.
var imageListColumns = []string{"name", "volsize", "used", "jaildeck:source", "jaildeck:sha512", "clones"}

// Images lists the zvols directly under the store's dataset, each with its
// @base and the clones of it, in one `zfs list`. A dataset that does not
// exist yet is no images: nothing has been prepared.
func (a *ImageAdapter) Images(ctx context.Context, store images.Store) ([]images.Image, error) {
	result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: []string{
		"list", "-H", "-p", "-t", "volume,snapshot", "-o", strings.Join(imageListColumns, ","), "-r", store.Dataset,
	}})
	if err != nil {
		if exited(result, err) && strings.Contains(result.Stderr, "dataset does not exist") {
			return make([]images.Image, 0), nil
		}
		return nil, err
	}
	return parseImageList(result.Stdout, store.Dataset)
}

// parseImageList reads Images' zfs list. Volumes come before their snapshots
// in zfs's order, but nothing here depends on that.
func parseImageList(out, dataset string) ([]images.Image, error) {
	found := make([]images.Image, 0)
	at := map[string]int{}
	snapshots := [][]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != len(imageListColumns) {
			return nil, fmt.Errorf("zfs list: %d columns in %q, want %d", len(fields), line, len(imageListColumns))
		}
		if strings.Contains(fields[0], "@") {
			snapshots = append(snapshots, fields)
			continue
		}
		name, ok := strings.CutPrefix(fields[0], dataset+"/")
		if !ok || strings.Contains(name, "/") {
			continue // the dataset itself, or something deeper than an image
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("zfs list: volsize of %s: %w", fields[0], err)
		}
		used, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("zfs list: used of %s: %w", fields[0], err)
		}
		at[fields[0]] = len(found)
		found = append(found, images.Image{
			Name: name, Dataset: fields[0], Size: size, Used: used,
			Source: dashless(fields[3]), SHA512: dashless(fields[4]),
			Guests: make([]string, 0),
		})
	}
	datastore := path.Dir(dataset)
	for _, fields := range snapshots {
		volume, snapshot, _ := strings.Cut(fields[0], "@")
		i, ok := at[volume]
		if !ok || snapshot != images.ReadySnapshot {
			continue
		}
		found[i].Ready = true
		found[i].Snapshot = fields[0]
		for _, clone := range strings.Split(dashless(fields[5]), ",") {
			if clone != "" {
				found[i].Guests = append(found[i].Guests, guestOf(clone, datastore))
			}
		}
	}
	return found, nil
}

// guestOf names the guest a clone belongs to: <datastore>/<guest>/disk0 is
// where vm-bhyve keeps a guest's disk. A clone anywhere else is named as
// itself, which is still the answer to "what uses this image".
func guestOf(clone, datastore string) string {
	rest, ok := strings.CutPrefix(clone, datastore+"/")
	if !ok {
		return clone
	}
	guest, _, _ := strings.Cut(rest, "/")
	return guest
}

// Fetched lists the sources whose archive is in the store's directory. Only
// a verified archive is ever there under its own name: Fetch downloads to
// .part and renames once the checksum matches.
func (a *ImageAdapter) Fetched(_ context.Context, store images.Store) ([]string, error) {
	out := make([]string, 0)
	for _, entry := range imageCatalog {
		if a.exists(path.Join(store.Path, entry.archive)) {
			out = append(out, entry.Name)
		}
	}
	return out, nil
}

// Fetch downloads a source's archive and verifies it against the checksum
// file published beside it (JD-037).
//
// Debian publishes no signature for these, so this guards against a corrupt
// download, not a dishonest server -- the release template's MANIFEST has
// the same strength. The archive arrives as <archive>.part, which `fetch -a`
// resumes, and becomes <archive> only once it matches: an archive under its
// own name is a verified one, and that is what Fetched reads.
func (a *ImageAdapter) Fetch(ctx context.Context, store images.Store, source string) (string, error) {
	entry, err := lookupSource(source)
	if err != nil {
		return "", err
	}
	archive := path.Join(store.Path, entry.archive)
	part := archive + ".part"
	download := system.Command{Name: "fetch", Args: []string{"-a", "-o", part, entry.URL}, Timeout: imageFetchTimeout}

	if err := a.ensureImageDataset(ctx, store); err != nil {
		return download.String(), err
	}
	sums, err := a.runHost(ctx, system.Command{
		Name: "fetch", Args: []string{"-q", "-o", "-", entry.dir + "/" + entry.sums}, Timeout: time.Minute,
	})
	if err != nil {
		return download.String(), err
	}
	want, err := sha512SumsEntry(sums.Stdout, entry.archive)
	if err != nil {
		return download.String(), err
	}
	if _, err := a.runHost(ctx, download); err != nil {
		return download.String(), err
	}
	got, err := a.runHost(ctx, system.Command{Name: "sha512", Args: []string{"-q", part}, Timeout: 5 * time.Minute})
	if err != nil {
		return download.String(), err
	}
	if sum := strings.TrimSpace(got.Stdout); sum != want {
		// Removed, or fetch -a would resume a corrupt file next time.
		if _, rmErr := a.runHost(ctx, system.Command{Name: "rm", Args: []string{"-f", part}}); rmErr != nil {
			common.Logger(ctx, "images").Warn("could not remove a corrupt download", "file", part, "err", rmErr)
		}
		return download.String(), common.NewAppError(http.StatusBadGateway, fmt.Sprintf(
			"%s does not match the checksum %s publishes for it (got %s, want %s); the download was removed",
			entry.archive, entry.sums, sum, want))
	}
	_, err = a.runHost(ctx, system.Command{Name: "mv", Args: []string{part, archive}})
	return download.String(), err
}

// ensureImageDataset creates <datastore>/.images the first time, with
// volmode=dev for every image under it to inherit (JD-037). Its mount point
// is inherited too, which is the directory the archives go in.
func (a *ImageAdapter) ensureImageDataset(ctx context.Context, store images.Store) error {
	result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: []string{"list", "-H", "-o", "name", store.Dataset}})
	if err == nil {
		return nil
	}
	if !exited(result, err) || !strings.Contains(result.Stderr, "dataset does not exist") {
		return err
	}
	_, err = a.runHost(ctx, system.Command{Name: "zfs", Args: []string{"create", "-o", "volmode=dev", store.Dataset}})
	return err
}

// sha512SumsEntry reads one file's checksum out of a SHA512SUMS file, in
// sha512sum's format: the hex digest, two spaces, the file name.
func sha512SumsEntry(sums, file string) (string, error) {
	for _, line := range nonEmptyLines(sums) {
		digest, name, ok := strings.Cut(line, "  ")
		if ok && strings.TrimPrefix(name, "*") == file && len(digest) == 128 {
			return digest, nil
		}
	}
	return "", common.NewAppError(http.StatusBadGateway, "the checksum file names no SHA-512 for "+file)
}

// Write creates the image's zvol and writes the archive's disk into it
// (JD-037):
//
//	zfs create -s -V <size> -o volmode=dev -o jaildeck:source=… -o jaildeck:sha512=… <dataset>
//	tar -xOf <archive> disk.raw > /dev/zvol/<dataset>
//
// The size is the disk's, from tar's listing. The checksum recorded is the
// archive's own, taken again here rather than trusted from the fetch: it is
// what names the bytes that were actually written. tar writes the disk to
// its standard output, which the runner points at the zvol, so there is no
// shell, no dd and no temporary file.
func (a *ImageAdapter) Write(ctx context.Context, store images.Store, name, source string) (string, error) {
	entry, err := lookupSource(source)
	if err != nil {
		return "", err
	}
	archive := path.Join(store.Path, entry.archive)
	dataset := store.Dataset + "/" + name
	device := "/dev/zvol/" + dataset
	write := system.Command{Name: "tar", Args: []string{"-xOf", archive, entry.disk},
		StdoutFile: device, Timeout: imageWriteTimeout}

	listing, err := a.runHost(ctx, system.Command{Name: "tar", Args: []string{"-tvf", archive}, Timeout: imageReadTimeout})
	if err != nil {
		return write.String(), err
	}
	size, err := tarMemberSize(listing.Stdout, entry.disk)
	if err != nil {
		return write.String(), err
	}
	sum, err := a.runHost(ctx, system.Command{Name: "sha512", Args: []string{"-q", archive}, Timeout: 5 * time.Minute})
	if err != nil {
		return write.String(), err
	}
	if _, err := a.runHost(ctx, system.Command{Name: "zfs", Args: []string{
		"create", "-s", "-V", strconv.FormatInt(size, 10), "-o", "volmode=dev",
		"-o", "jaildeck:source=" + source, "-o", "jaildeck:sha512=" + strings.TrimSpace(sum.Stdout), dataset,
	}}); err != nil {
		return write.String(), err
	}
	if err := a.waitFor(ctx, device); err != nil {
		return write.String(), err
	}
	_, err = a.runHost(ctx, write)
	return write.String(), err
}

// waitFor gives a new zvol's device node time to appear.
func (a *ImageAdapter) waitFor(ctx context.Context, device string) error {
	deadline := time.Now().Add(zvolDeviceWait)
	for !a.exists(device) {
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not appear within %s of creating its zvol", device, zvolDeviceWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil
}

// tarMemberSize reads a member's size out of `tar -tvf`, bsdtar's ls-like
// listing:
//
//	-rw-r--r--  0 root   root 3221225472 Oct 21 02:00 disk.raw
func tarMemberSize(listing, member string) (int64, error) {
	for _, line := range nonEmptyLines(listing) {
		fields := strings.Fields(line)
		if len(fields) < 9 || fields[len(fields)-1] != member {
			continue
		}
		size, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("tar -tv: size of %s: %w", member, err)
		}
		return size, nil
	}
	return 0, errors.New("the archive has no " + member)
}

// Seal takes @base, which makes the image usable.
func (a *ImageAdapter) Seal(ctx context.Context, store images.Store, name string) (string, error) {
	cmd := system.Command{Name: "zfs", Args: []string{"snapshot", store.Dataset + "/" + name + "@" + images.ReadySnapshot}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

// Destroy removes the zvol and its snapshots. Never -R: that would take the
// guests' disks cloned from it, which the service refuses to touch and zfs
// refuses without.
func (a *ImageAdapter) Destroy(ctx context.Context, store images.Store, name string) (string, error) {
	cmd := system.Command{Name: "zfs", Args: []string{"destroy", "-r", store.Dataset + "/" + name}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

var _ images.ImageSystem = (*ImageAdapter)(nil)
