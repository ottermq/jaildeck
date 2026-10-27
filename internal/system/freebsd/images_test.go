package freebsd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/images"
	"github.com/ottermq/jaildeck/internal/system"
)

// From capture-vm-images-induced.sh (a real Debian 13 image fetched, written
// and booted) and capture-vm-image-list-induced.sh (small zvols read with the
// adapter's own commands), both on isengard.

// imageRunner replays captures by command line and answers the few commands
// no capture has with a constructed result, which each test names as such.
// It keeps every command as the adapter built it.
type imageRunner struct {
	replay      *replayRunner
	constructed map[string]system.CommandResult
	ran         []system.Command
}

func (r *imageRunner) Run(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
	r.ran = append(r.ran, cmd)
	if result, ok := r.constructed[cmd.String()]; ok {
		if result.ExitCode != 0 {
			return result, errors.New("exit status 1")
		}
		return result, nil
	}
	return r.replay.Run(ctx, cmd)
}

func (r *imageRunner) lines() []string {
	out := make([]string, 0, len(r.ran))
	for _, cmd := range r.ran {
		out = append(out, cmd.String())
	}
	return out
}

func imageAdapter(t *testing.T, captures map[string]string, constructed map[string]system.CommandResult) (*ImageAdapter, *imageRunner) {
	t.Helper()
	runner := &imageRunner{replay: &replayRunner{t: t, set: vmSet, captures: captures}, constructed: constructed}
	a := NewImageAdapter(runner)
	a.exists = func(string) bool { return true }
	return a, runner
}

var isengardImages = images.Store{VMBhyve: images.VMBhyveReady, Dataset: "tank/vm/.images", Path: "/vm/.images"}

const (
	debianArchive = "/vm/.images/debian-13-genericcloud-amd64.tar.xz"
	debianURL     = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.tar.xz"
)

func capturedSHA512(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(fixture(vmSet, "vm_img_sha512", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// --- the store -------------------------------------------------------------

func TestImageStoreIsDotImagesInTheDefaultDatastore(t *testing.T) {
	a, _ := imageAdapter(t, map[string]string{
		"vm list":           "vm_ready_list",
		"vm datastore list": "vm_ready_datastore",
	}, nil)
	store, err := a.Store(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := images.Store{VMBhyve: images.VMBhyveReady, Dataset: "tank/jd-vmcap/.images", Path: "/jd-vmcap/.images"}
	if store != want || !store.Usable() {
		t.Errorf("store = %+v, want %+v and usable", store, want)
	}
}

func TestImageStoreSaysWhyVMBhyveCannotHoldImages(t *testing.T) {
	a, _ := imageAdapter(t, map[string]string{"vm list": "vm_installed_list"}, nil)
	store, err := a.Store(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if store.VMBhyve != images.VMBhyveNotConfigured || store.Problem != "$vm_enable is not enabled in /etc/rc.conf!" || store.Usable() {
		t.Errorf("store = %+v", store)
	}

	notInstalled := NewImageAdapter(&replayRunner{t: t, set: "rohan", captures: map[string]string{"vm list": "vm_list"}})
	store, err = notInstalled.Store(context.Background())
	if err != nil || store.VMBhyve != images.VMBhyveNotInstalled || store.Usable() {
		t.Errorf("store = %+v, err = %v; want not installed", store, err)
	}
}

// Constructed: no capture has a datastore that is a plain directory. vm-bhyve
// prints "-" in the dataset column for one.
func TestImageStoreRefusesADatastoreNotOnZFS(t *testing.T) {
	a, _ := imageAdapter(t, map[string]string{"vm list": "vm_ready_list"}, map[string]system.CommandResult{
		"vm datastore list": {Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
			"default         directory   /vm                       -\n"},
	})
	store, err := a.Store(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if store.Usable() || store.Dataset != "" || !strings.Contains(store.Problem, "/vm, is not on ZFS") {
		t.Errorf("store = %+v", store)
	}
}

// --- the listing -----------------------------------------------------------

func TestImagesReadsEachZvolItsBaseAndItsGuests(t *testing.T) {
	a, _ := imageAdapter(t, map[string]string{
		"zfs list -H -p -t volume,snapshot -o name,volsize,used,jaildeck:source,jaildeck:sha512,clones -r tank/vm/.images": "vm_imgls_list",
	}, nil)
	found, err := a.Images(context.Background(), isengardImages)
	if err != nil {
		t.Fatal(err)
	}
	sum := strings.Repeat("0123456789abcdef", 8)
	want := []images.Image{
		{Name: "jd-cap-ready", Source: "debian-13", Dataset: "tank/vm/.images/jd-cap-ready",
			Ready: true, Snapshot: "tank/vm/.images/jd-cap-ready@base",
			Size: 67108864, Used: 57344, SHA512: sum, Guests: []string{"jd-cap-g"}},
		{Name: "jd-cap-written", Source: "debian-13", Dataset: "tank/vm/.images/jd-cap-written",
			Size: 67108864, Used: 57344, SHA512: sum, Guests: []string{}},
	}
	if len(found) != len(want) {
		t.Fatalf("found %d images, want %d: %+v", len(found), len(want), found)
	}
	for i := range want {
		got := found[i]
		if got.Name != want[i].Name || got.Source != want[i].Source || got.Dataset != want[i].Dataset ||
			got.Ready != want[i].Ready || got.Snapshot != want[i].Snapshot || got.Size != want[i].Size ||
			got.Used != want[i].Used || got.SHA512 != want[i].SHA512 || !slices.Equal(got.Guests, want[i].Guests) {
			t.Errorf("image %d = %+v\nwant %+v", i, got, want[i])
		}
	}
}

// Constructed: a volume deeper than .images/<name>, which only someone
// creating a filesystem under .images by hand would make, is not an image.
func TestImagesSkipsWhatIsNotDirectlyUnderTheDataset(t *testing.T) {
	out := "tank/vm/.images/by-hand/vol\t67108864\t57344\t-\t-\t-\n" +
		"tank/vm/.images/by-hand/vol@base\t67108864\t0\t-\t-\t-\n"
	found, err := parseImageList(out, "tank/vm/.images")
	if err != nil || len(found) != 0 {
		t.Errorf("found = %+v, err = %v; want nothing", found, err)
	}
}

func TestImagesWithNoDatasetOrNoImagesIsAnEmptyList(t *testing.T) {
	for _, capture := range []string{"vm_imgls_absent", "vm_imgls_empty"} {
		t.Run(capture, func(t *testing.T) {
			a, _ := imageAdapter(t, map[string]string{
				"zfs list -H -p -t volume,snapshot -o name,volsize,used,jaildeck:source,jaildeck:sha512,clones -r tank/vm/.images": capture,
			}, nil)
			found, err := a.Images(context.Background(), isengardImages)
			if err != nil || found == nil || len(found) != 0 {
				t.Errorf("found = %#v, err = %v; want an empty list", found, err)
			}
		})
	}
}

func TestGuestOfAClone(t *testing.T) {
	cases := map[string]string{
		"tank/vm/jd-cap-g/disk0": "jd-cap-g",
		"tank/elsewhere/disk":    "tank/elsewhere/disk",
	}
	for clone, want := range cases {
		if got := guestOf(clone, "tank/vm"); got != want {
			t.Errorf("guestOf(%q) = %q, want %q", clone, got, want)
		}
	}
}

func TestFetchedIsAnArchiveUnderItsOwnName(t *testing.T) {
	dir := t.TempDir()
	a := NewImageAdapter(nil)
	store := images.Store{Path: dir}
	archive := filepath.Join(dir, "debian-13-genericcloud-amd64.tar.xz")

	if err := os.WriteFile(archive+".part", []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fetched, _ := a.Fetched(context.Background(), store); len(fetched) != 0 {
		t.Errorf("a .part counted as fetched: %v", fetched)
	}
	if err := os.Rename(archive+".part", archive); err != nil {
		t.Fatal(err)
	}
	if fetched, _ := a.Fetched(context.Background(), store); !slices.Equal(fetched, []string{"debian-13"}) {
		t.Errorf("fetched = %v, want debian-13", fetched)
	}
}

// --- what the published files say -----------------------------------------

func TestSHA512SumsEntry(t *testing.T) {
	line, err := os.ReadFile(fixture(vmSet, "vm_img_sums_line", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	// The captured line, among constructed neighbours that are not the file.
	sums := strings.Repeat("a", 128) + "  debian-13-genericcloud-amd64.qcow2\n" + string(line) +
		strings.Repeat("b", 128) + "  debian-13-genericcloud-amd64.tar.xz.json\n"
	got, err := sha512SumsEntry(sums, "debian-13-genericcloud-amd64.tar.xz")
	if err != nil || got != capturedSHA512(t) {
		t.Errorf("sha512SumsEntry = %q, %v; want the archive's", got, err)
	}

	_, err = sha512SumsEntry(sums, "debian-13-genericcloud-amd64.raw")
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusBadGateway {
		t.Errorf("err = %v, want a 502 for a file the checksums do not name", err)
	}
}

func TestTarMemberSize(t *testing.T) {
	listing, err := os.ReadFile(fixture(vmSet, "vm_img_tar_list", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if size, err := tarMemberSize(string(listing), "disk.raw"); err != nil || size != 3221225472 {
		t.Errorf("size = %d, %v; want 3221225472", size, err)
	}
	if _, err := tarMemberSize(string(listing), "disk.qcow2"); err == nil {
		t.Error("a member the archive does not have was given a size")
	}
}

// --- fetch -----------------------------------------------------------------

func fetchCaptures(dataset string) map[string]string {
	return map[string]string{
		"zfs list -H -o name tank/vm/.images":                                          dataset,
		"zfs create -o volmode=dev tank/vm/.images":                                    "vm_imgls_create_dataset",
		"fetch -q -o - https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS": "vm_img_sums_line",
		"fetch -a -o " + debianArchive + ".part " + debianURL:                          "vm_img_fetch",
		"sha512 -q " + debianArchive + ".part":                                         "vm_img_sha512",
	}
}

// Constructed: mv's success, which prints nothing.
var mvArchive = map[string]system.CommandResult{"mv " + debianArchive + ".part " + debianArchive: {}}

func TestFetchCreatesTheDatasetAndKeepsOnlyAVerifiedArchive(t *testing.T) {
	a, runner := imageAdapter(t, fetchCaptures("vm_imgls_dataset_absent"), mvArchive)
	command, err := a.Fetch(context.Background(), isengardImages, "debian-13")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"zfs list -H -o name tank/vm/.images",
		"zfs create -o volmode=dev tank/vm/.images",
		"fetch -q -o - https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS",
		"fetch -a -o " + debianArchive + ".part " + debianURL,
		"sha512 -q " + debianArchive + ".part",
		"mv " + debianArchive + ".part " + debianArchive,
	}
	if got := runner.lines(); !slices.Equal(got, want) {
		t.Errorf("ran\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if command != "fetch -a -o "+debianArchive+".part "+debianURL {
		t.Errorf("command = %q", command)
	}
	if download := runner.ran[3]; download.Timeout != imageFetchTimeout {
		t.Errorf("the download's timeout = %s, want %s", download.Timeout, imageFetchTimeout)
	}
}

func TestFetchLeavesAnExistingDatasetAlone(t *testing.T) {
	a, runner := imageAdapter(t, fetchCaptures("vm_imgls_dataset_present"), mvArchive)
	if _, err := a.Fetch(context.Background(), isengardImages, "debian-13"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(runner.lines(), "zfs create -o volmode=dev tank/vm/.images") {
		t.Error("created .images although it was there")
	}
}

// Constructed: a download whose checksum is not the published one.
func TestFetchRemovesADownloadThatDoesNotMatch(t *testing.T) {
	captures := fetchCaptures("vm_imgls_dataset_present")
	delete(captures, "sha512 -q "+debianArchive+".part")
	a, runner := imageAdapter(t, captures, map[string]system.CommandResult{
		"sha512 -q " + debianArchive + ".part": {Stdout: strings.Repeat("f", 128) + "\n"},
		"rm -f " + debianArchive + ".part":     {},
	})
	_, err := a.Fetch(context.Background(), isengardImages, "debian-13")
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusBadGateway {
		t.Fatalf("err = %v, want a 502", err)
	}
	ran := runner.lines()
	if !slices.Contains(ran, "rm -f "+debianArchive+".part") {
		t.Error("the corrupt download was kept, and fetch -a would resume it")
	}
	if slices.ContainsFunc(ran, func(line string) bool { return strings.HasPrefix(line, "mv ") }) {
		t.Error("a download that does not match was given the archive's name")
	}
}

// --- write, seal, destroy ----------------------------------------------------

func writeCaptures(t *testing.T) map[string]string {
	return map[string]string{
		"tar -tvf " + debianArchive:  "vm_img_tar_list",
		"sha512 -q " + debianArchive: "vm_img_sha512",
		"zfs create -s -V 3221225472 -o volmode=dev -o jaildeck:source=debian-13 -o jaildeck:sha512=" +
			capturedSHA512(t) + " tank/vm/.images/debian-13": "vm_img_create_zvol",
		"tar -xOf " + debianArchive + " disk.raw > /dev/zvol/tank/vm/.images/debian-13": "vm_img_write",
	}
}

func TestWriteMakesADevModeZvolAndTarWritesStraightIntoIt(t *testing.T) {
	a, runner := imageAdapter(t, writeCaptures(t), nil)
	command, err := a.Write(context.Background(), isengardImages, "debian-13", "debian-13")
	if err != nil {
		t.Fatal(err)
	}
	if command != "tar -xOf "+debianArchive+" disk.raw > /dev/zvol/tank/vm/.images/debian-13" {
		t.Errorf("command = %q", command)
	}
	last := runner.ran[len(runner.ran)-1]
	if last.StdoutFile != "/dev/zvol/tank/vm/.images/debian-13" || last.Timeout != imageWriteTimeout {
		t.Errorf("the write = %+v; want tar's output into the zvol, under the write's timeout", last)
	}
	if len(runner.ran) != 4 {
		t.Errorf("ran %d commands, want 4: %v", len(runner.ran), runner.lines())
	}
}

func TestWriteWaitsForTheZvolsDeviceAndGivesUp(t *testing.T) {
	a, runner := imageAdapter(t, writeCaptures(t), nil)
	a.exists = func(string) bool { return false }
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := a.Write(ctx, isengardImages, "debian-13", "debian-13"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the wait to end with the context", err)
	}
	for _, cmd := range runner.ran {
		if cmd.StdoutFile != "" {
			t.Errorf("tar ran with no device to write into: %s", cmd)
		}
	}
}

func TestSealAndDestroy(t *testing.T) {
	a, _ := imageAdapter(t, map[string]string{
		"zfs snapshot tank/vm/.images/jd-cap-ready@base": "vm_imgls_seal",
		"zfs destroy -r tank/vm/.images/jd-cap-written":  "vm_imgls_destroy_written",
		"zfs destroy -r tank/vm/.images/jd-cap-ready":    "vm_imgls_destroy_in_use",
	}, nil)
	ctx := context.Background()
	if command, err := a.Seal(ctx, isengardImages, "jd-cap-ready"); err != nil || command != "zfs snapshot tank/vm/.images/jd-cap-ready@base" {
		t.Errorf("Seal = %q, %v", command, err)
	}
	if _, err := a.Destroy(ctx, isengardImages, "jd-cap-written"); err != nil {
		t.Errorf("Destroy: %v", err)
	}
	// zfs's own refusal, for the image a guest's disk is cloned from.
	_, err := a.Destroy(ctx, isengardImages, "jd-cap-ready")
	if err == nil || !strings.Contains(err.Error(), "dependent clones") {
		t.Errorf("err = %v, want zfs's refusal", err)
	}
}

func TestEveryCatalogSourceSaysWhereItComesFrom(t *testing.T) {
	for _, source := range NewImageAdapter(nil).Sources() {
		if source.Name == "" || source.Description == "" || !strings.HasPrefix(source.URL, "https://") {
			t.Errorf("source = %+v", source)
		}
	}
}
