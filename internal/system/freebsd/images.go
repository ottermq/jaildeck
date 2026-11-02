package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// for it, and how the disk is read out of it (JD-037, JD-053).
type imageSource struct {
	images.Source
	// dir is the directory the archive and its checksum file are published
	// in.
	dir     string
	archive string
	// sums is the checksum file, in dir, and sum the digest it carries:
	// sha256 or sha512.
	sums string
	sum  string
	// format is how the disk is in the archive: diskTarRaw, a raw disk as a
	// tar member named disk, or diskQcow2, the archive itself a qcow2 image.
	format string
	disk   string
	// signed is what the publisher signs with key (JD-054): sigSums, the
	// checksum file, with a detached signature at sig in dir; sigCleartext,
	// the checksum file signed in place; or sigArchive, the archive, with a
	// detached signature at sig. Empty for a publisher that signs nothing,
	// which Debian's cloud images are.
	signed string
	sig    string
	key    signingKey
}

const (
	sigSums      = "sums"
	sigCleartext = "cleartext"
	sigArchive   = "archive"
)

// The publishers' keys, each checked against where its publisher names it
// when it was added (JD-054).
var (
	ubuntuCloudKey = signingKey{file: "ubuntu-cloud.asc", name: "Ubuntu's cloud image key",
		fingerprint: "d2eb44626fddc30b513d5bb71a5d6c4c7db87c81"}
	fedora44Key = signingKey{file: "fedora-44.asc", name: "Fedora 44's key",
		fingerprint: "36f612dcf27f7d1a48a835e4dbfcf71c6d9f90a6"}
	alpineCloudKey = signingKey{file: "alpine-cloud.asc", name: "Alpine's cloud image key",
		fingerprint: "f26adfadbae702ef7af637459da7ef23bffcdf22"}
	// arch-boxes signs with an Ed25519 subkey of 1b9a16984a4e8cb448712d2ae0b78bf4326c6f8f.
	archBoxesKey = signingKey{file: "arch-boxes.asc", name: "Arch's arch-boxes key",
		fingerprint: "656e4c5ac1cc3b86e539d97e343635a6859a9174"}
)

const (
	diskTarRaw = "tar-raw"
	diskQcow2  = "qcow2"
)

// imageCatalog is every image Jail Deck can prepare. Debian publishes its
// cloud images in three forms; the .tar.xz is the smallest and tar in base
// reads it (JD-019, JD-037). The others publish qcow2, which Jail Deck reads
// itself (qcow2.go, JD-053). Each was booted to SSH under bhyve's UEFI
// firmware with vm-bhyve's NoCloud seed before it was listed here.
var imageCatalog = []imageSource{
	{
		Source: images.Source{
			Name:        "debian-13",
			Description: "Debian 13 (trixie), genericcloud, amd64, with cloud-init",
			URL:         debianTrixie + "/debian-13-genericcloud-amd64.tar.xz",
			User:        "debian",
		},
		dir:     debianTrixie,
		archive: "debian-13-genericcloud-amd64.tar.xz",
		sums:    "SHA512SUMS",
		sum:     "sha512",
		format:  diskTarRaw,
		disk:    "disk.raw",
	},
	{
		Source: images.Source{
			Name:        "ubuntu-26.04",
			Description: "Ubuntu 26.04 LTS, server cloud image, amd64, with cloud-init",
			URL:         ubuntu2604 + "/ubuntu-26.04-server-cloudimg-amd64.img",
			User:        "ubuntu",
		},
		dir:     ubuntu2604,
		archive: "ubuntu-26.04-server-cloudimg-amd64.img",
		sums:    "SHA256SUMS",
		sum:     "sha256",
		format:  diskQcow2,
		signed:  sigSums,
		sig:     "SHA256SUMS.gpg",
		key:     ubuntuCloudKey,
	},
	{
		Source: images.Source{
			Name:        "fedora-44",
			Description: "Fedora 44 Cloud Base, generic, x86_64, with cloud-init",
			URL:         fedora44 + "/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
			User:        "fedora",
		},
		dir:     fedora44,
		archive: "Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
		sums:    "Fedora-Cloud-44-1.7-x86_64-CHECKSUM",
		sum:     "sha256",
		format:  diskQcow2,
		signed:  sigCleartext,
		key:     fedora44Key,
	},
	{
		Source: images.Source{
			Name:        "alpine-3.24",
			Description: "Alpine Linux 3.24, generic UEFI cloud image, x86_64, with cloud-init",
			URL:         alpine324 + "/" + alpineArchive,
			User:        "alpine",
		},
		dir:     alpine324,
		archive: alpineArchive,
		sums:    alpineArchive + ".sha512",
		sum:     "sha512",
		format:  diskQcow2,
		signed:  sigArchive,
		sig:     alpineArchive + ".asc",
		key:     alpineCloudKey,
	},
	{
		Source: images.Source{
			Name:        "arch",
			Description: "Arch Linux, cloud image (rolling), x86_64, with cloud-init",
			URL:         archLatest + "/Arch-Linux-x86_64-cloudimg.qcow2",
			User:        "arch",
		},
		dir:     archLatest,
		archive: "Arch-Linux-x86_64-cloudimg.qcow2",
		sums:    "Arch-Linux-x86_64-cloudimg.qcow2.SHA256",
		sum:     "sha256",
		format:  diskQcow2,
		signed:  sigArchive,
		sig:     "Arch-Linux-x86_64-cloudimg.qcow2.sig",
		key:     archBoxesKey,
	},
}

// Where each is published. Debian's "latest" and Arch's move with each
// build; the others name a release. The checksum recorded on an image names
// the one it was written from.
const (
	debianTrixie  = "https://cloud.debian.org/images/cloud/trixie/latest"
	ubuntu2604    = "https://cloud-images.ubuntu.com/releases/resolute/release"
	fedora44      = "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images"
	alpine324     = "https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/cloud"
	alpineArchive = "generic_alpine-3.24.1-x86_64-uefi-cloudinit-r0.qcow2"
	archLatest    = "https://geo.mirror.pkgbuild.com/images/latest"
)

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
	// qcow2Size and writeQcow2 read a qcow2 archive and write its disk into
	// a device; overridden in tests, for the same reason.
	qcow2Size  func(path string) (int64, error)
	writeQcow2 func(ctx context.Context, path, device string) (int64, error)
	// openArchive opens a downloaded archive to check its signature;
	// overridden in tests, which have no archives.
	openArchive func(path string) (io.ReadCloser, error)
}

func NewImageAdapter(runner system.CommandRunner) *ImageAdapter {
	return &ImageAdapter{Adapter: NewAdapter(runner), exists: pathExists,
		qcow2Size: qcow2Size, writeQcow2: writeQcow2,
		openArchive: func(p string) (io.ReadCloser, error) { return os.Open(p) }}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (a *ImageAdapter) Sources() []images.Source {
	out := make([]images.Source, 0, len(imageCatalog))
	for _, entry := range imageCatalog {
		source := entry.Source
		source.SignedBy = entry.key.name
		out = append(out, source)
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
// file published beside it (JD-037), and against its publisher's signature
// where the publisher signs (JD-054): the checksum file, or the archive
// itself.
//
// Debian publishes no signature for its cloud images, so for Debian this
// guards against a corrupt download, not a dishonest server -- the release
// template's MANIFEST has the same strength. The archive arrives as
// <archive>.part, which `fetch -a` resumes, and becomes <archive> only once
// it matches: an archive under its own name is a verified one, and that is
// what Fetched reads.
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
	sumsText := sums.Stdout
	var archiveSig []byte
	switch entry.signed {
	case sigSums, sigArchive:
		// Read before the download, so a missing signature costs a second,
		// not the archive.
		sig, err := a.runHost(ctx, system.Command{
			Name: "fetch", Args: []string{"-q", "-o", "-", entry.dir + "/" + entry.sig}, Timeout: time.Minute,
		})
		if err != nil {
			return download.String(), err
		}
		if entry.signed == sigArchive {
			archiveSig = []byte(sig.Stdout)
		} else if err := verifyDetached(entry.key, strings.NewReader(sumsText), []byte(sig.Stdout)); err != nil {
			return download.String(), notSigned(entry.sums, entry.key, err)
		}
	case sigCleartext:
		// Only the signed text is read for the digest.
		if sumsText, err = verifyCleartext(entry.key, []byte(sumsText)); err != nil {
			return download.String(), notSigned(entry.sums, entry.key, err)
		}
	}
	want, err := sumsEntry(sumsText, entry.archive, entry.sum)
	if err != nil {
		return download.String(), err
	}
	if _, err := a.runHost(ctx, download); err != nil {
		return download.String(), err
	}
	got, err := a.runHost(ctx, system.Command{Name: entry.sum, Args: []string{"-q", part}, Timeout: 5 * time.Minute})
	if err != nil {
		return download.String(), err
	}
	if sum := strings.TrimSpace(got.Stdout); sum != want {
		a.removeDownload(ctx, part)
		return download.String(), common.NewAppError(http.StatusBadGateway, fmt.Sprintf(
			"%s does not match the checksum %s publishes for it (got %s, want %s); the download was removed",
			entry.archive, entry.sums, sum, want))
	}
	if entry.signed == sigArchive {
		if err := a.verifyArchive(part, entry.key, archiveSig); err != nil {
			a.removeDownload(ctx, part)
			return download.String(), common.NewAppError(http.StatusBadGateway, fmt.Sprintf(
				"%s is not signed by %s (%v); the download was removed", entry.archive, entry.key.name, err))
		}
	}
	_, err = a.runHost(ctx, system.Command{Name: "mv", Args: []string{part, archive}})
	return download.String(), err
}

func (a *ImageAdapter) verifyArchive(part string, key signingKey, sig []byte) error {
	f, err := a.openArchive(part)
	if err != nil {
		return err
	}
	defer f.Close()
	return verifyDetached(key, f, sig)
}

// removeDownload removes a download that failed its check, or fetch -a would
// resume it next time.
func (a *ImageAdapter) removeDownload(ctx context.Context, part string) {
	if _, err := a.runHost(ctx, system.Command{Name: "rm", Args: []string{"-f", part}}); err != nil {
		common.Logger(ctx, "images").Warn("could not remove a download that failed its check", "file", part, "err", err)
	}
}

// notSigned is the refusal for a checksum file its publisher's key did not
// sign. A 502, like a download that does not match: the publisher's side is
// what is wrong, or something between it and this host.
func notSigned(file string, key signingKey, err error) error {
	return common.NewAppError(http.StatusBadGateway, fmt.Sprintf("%s is not signed by %s (%v)", file, key.name, err))
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

// sumsEntry reads one file's digest out of a publisher's checksum file. The
// four forms published are all read:
//
//	<hex>  <file>              sha512sum's, Debian's and Arch's
//	<hex> *<file>              the same in binary mode, Ubuntu's
//	SHA256 (<file>) = <hex>    BSD's, Fedora's, inside a signed message
//	<hex>                      one file's own, Alpine's
//
// The digest must be the algorithm's length, so a SHA-256 is never taken
// for a SHA-512 or a line of some other file's.
func sumsEntry(sums, file, sum string) (string, error) {
	length := map[string]int{"sha256": 64, "sha512": 128}[sum]
	hex := func(s string) bool {
		if len(s) != length {
			return false
		}
		for _, c := range s {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return false
			}
		}
		return true
	}
	lines := nonEmptyLines(sums)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if digest, name, ok := strings.Cut(line, " "); ok && hex(digest) &&
			strings.TrimPrefix(strings.TrimSpace(name), "*") == file {
			return digest, nil
		}
		if rest, ok := strings.CutPrefix(line, strings.ToUpper(sum)+" ("+file+") = "); ok && hex(rest) {
			return rest, nil
		}
	}
	if len(lines) == 1 && hex(strings.TrimSpace(lines[0])) {
		return strings.TrimSpace(lines[0]), nil
	}
	return "", common.NewAppError(http.StatusBadGateway,
		fmt.Sprintf("the checksum file names no %s for %s", strings.ToUpper(sum), file))
}

// Write creates the image's zvol and writes the archive's disk into it
// (JD-037):
//
//	zfs create -s -V <size> -o volmode=dev -o jaildeck:source=… -o jaildeck:sha512=… <dataset>
//	tar -xOf <archive> disk.raw > /dev/zvol/<dataset>
//
// The size is the disk's, from tar's listing. The checksum recorded is the
// archive's own SHA-512, taken again here whatever the publisher signs with,
// rather than trusted from the fetch: it is what names the bytes that were
// actually written. tar writes the disk to its standard output, which the
// runner points at the zvol, so there is no shell, no dd and no temporary
// file.
//
// A qcow2 archive is read by Jail Deck itself (JD-053): its size from its
// header, its clusters written straight into the zvol, the holes left as the
// zeros a sparse zvol already reads as. There is no command to audit for
// that step, so what is audited says what was done in the same form.
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
	described := write.String()

	var size int64
	switch entry.format {
	case diskQcow2:
		described = "qcow2 " + archive + " > " + device
		if size, err = a.qcow2Size(archive); err != nil {
			return described, fmt.Errorf("%s: %w", archive, err)
		}
	default:
		listing, err := a.runHost(ctx, system.Command{Name: "tar", Args: []string{"-tvf", archive}, Timeout: imageReadTimeout})
		if err != nil {
			return described, err
		}
		if size, err = tarMemberSize(listing.Stdout, entry.disk); err != nil {
			return described, err
		}
	}
	sum, err := a.runHost(ctx, system.Command{Name: "sha512", Args: []string{"-q", archive}, Timeout: 5 * time.Minute})
	if err != nil {
		return described, err
	}
	if _, err := a.runHost(ctx, system.Command{Name: "zfs", Args: []string{
		"create", "-s", "-V", strconv.FormatInt(size, 10), "-o", "volmode=dev",
		"-o", "jaildeck:source=" + source, "-o", "jaildeck:sha512=" + strings.TrimSpace(sum.Stdout), dataset,
	}}); err != nil {
		return described, err
	}
	if err := a.waitFor(ctx, device); err != nil {
		return described, err
	}
	if entry.format == diskQcow2 {
		ctx, cancel := context.WithTimeout(ctx, imageWriteTimeout)
		defer cancel()
		if _, err := a.writeQcow2(ctx, archive, device); err != nil {
			return described, fmt.Errorf("writing %s into %s: %w", archive, device, err)
		}
		return described, nil
	}
	_, err = a.runHost(ctx, write)
	return described, err
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
