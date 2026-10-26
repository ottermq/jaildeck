package fake

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/system"
)

// Images is an in-memory image store in a vm-bhyve datastore on ZFS. It
// starts with Debian 13 prepared and the fake's running guest cloned from it,
// so a removal that is refused can be seen as well as one that is not.
//
// The checksum is the archive capture-vm-images-induced.sh fetched on
// isengard.
//
// Its catalog has a second source that only the fake has, whose write fails:
// an image left written and not sealed is a state worth being able to see.
// JAILDECK_FAKE_FETCH_SECONDS makes each stage take time, as it does for
// release templates.
type Images struct {
	mu      sync.Mutex
	fetched map[string]bool
	images  map[string]*images.Image
	delay   time.Duration
}

// FailingImageSource is the fake's source whose write fails.
const FailingImageSource = "fails-to-write"

const fakeImageDataset = "zroot/vm/.images"

func NewImages() *Images {
	f := &Images{
		fetched: map[string]bool{"debian-13": true},
		images: map[string]*images.Image{
			"debian-13": {
				Name: "debian-13", Source: "debian-13", Dataset: fakeImageDataset + "/debian-13",
				Ready: true, Snapshot: fakeImageDataset + "/debian-13@base",
				Size: 3221225472, Used: 567738368,
				SHA512: "893deb857d128c3f52db3a1d0a941bd79a696dc0855f1e0925cc5be97eb898352ca40c46a822e039ddb1bacc30437c2c8ed46129500c21a841a3d7ee18c23c94",
				Guests: []string{"jd-cap-running"},
			},
		},
	}
	if seconds, err := strconv.Atoi(os.Getenv(fakeFetchSeconds)); err == nil && seconds > 0 {
		f.delay = time.Duration(seconds) * time.Second
	}
	return f
}

// SetDelay makes each stage take time. Zero returns at once.
func (f *Images) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *Images) Store(context.Context) (images.Store, error) {
	return images.Store{VMBhyve: images.VMBhyveReady, Dataset: fakeImageDataset, Path: "/vm/.images"}, nil
}

func (f *Images) Sources() []images.Source {
	return []images.Source{
		{Name: "debian-13", Description: "Debian 13 (trixie), genericcloud, amd64, with cloud-init",
			URL: "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.tar.xz"},
		{Name: FailingImageSource, Description: "Fake only: its write fails, leaving a zvol with no @base",
			URL: "https://example.invalid/fails-to-write.tar.xz"},
	}
}

func (f *Images) Images(context.Context, images.Store) ([]images.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]images.Image, 0, len(f.images))
	for _, image := range f.images {
		copied := *image
		copied.Guests = append(make([]string, 0, len(image.Guests)), image.Guests...)
		out = append(out, copied)
	}
	return out, nil
}

func (f *Images) Fetched(context.Context, images.Store) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.fetched))
	for source := range f.fetched {
		out = append(out, source)
	}
	return out, nil
}

func (f *Images) Fetch(ctx context.Context, _ images.Store, source string) (string, error) {
	command := "fetch -a -o /vm/.images/" + source + ".tar.xz.part https://example.invalid/" + source
	if err := f.wait(ctx); err != nil {
		return command, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched[source] = true
	return command, nil
}

func (f *Images) Write(ctx context.Context, store images.Store, name, source string) (string, error) {
	dataset := store.Dataset + "/" + name
	command := "tar -xOf /vm/.images/" + source + ".tar.xz disk.raw > /dev/zvol/" + dataset
	if err := f.wait(ctx); err != nil {
		return command, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[name] = &images.Image{Name: name, Source: source, Dataset: dataset,
		Size: 3221225472, Used: 1 << 20, Guests: []string{}}
	if source == FailingImageSource {
		// After the zvol exists, as the real write fails: tar is what goes
		// wrong, and the zvol it was writing into stays.
		const stderr = "tar: Error opening archive: Unrecognized archive format"
		return command, &system.CommandError{
			Command: "tar",
			Args:    []string{"-xOf", "/vm/.images/" + source + ".tar.xz", "disk.raw"},
			Result:  system.CommandResult{Stderr: stderr, ExitCode: 1},
			Err:     errors.New("exit status 1: " + stderr),
		}
	}
	f.images[name].Used = 567738368
	return command, nil
}

func (f *Images) Seal(_ context.Context, store images.Store, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot := store.Dataset + "/" + name + "@" + images.ReadySnapshot
	if image, ok := f.images[name]; ok {
		image.Ready, image.Snapshot = true, snapshot
	}
	return "zfs snapshot " + snapshot, nil
}

func (f *Images) Destroy(_ context.Context, store images.Store, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.images, name)
	return "zfs destroy -r " + store.Dataset + "/" + name, nil
}

func (f *Images) wait(ctx context.Context) error {
	f.mu.Lock()
	delay := f.delay
	f.mu.Unlock()
	if delay <= 0 {
		return nil
	}
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ images.ImageSystem = (*Images)(nil)
