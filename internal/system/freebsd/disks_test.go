package freebsd

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/system"
)

// Every disk here is a real one. isengard has four spinning disks, an NVMe, a
// DVD drive and an enclosure; rohan has a SATA SSD and a drive in a USB
// enclosure. Between them they carry every case the join has to survive, and
// none of them was constructed to (see testdata/hostfacts/README.md).

// diskCaptures answers every command Disks runs. The per-device reads are
// derived from the captured scan rather than listed, so a host set whose scan
// differs is replayed as that host rather than as this one.
func diskCaptures(t *testing.T, set string) map[string]string {
	t.Helper()
	captures := map[string]string{
		"smartctl --scan":          "smartctl_scan",
		"geom disk list":           "geom_disk_list",
		"zpool list -v -H -o name": "zpool_list_vdevs",
		"glabel status":            "glabel_status",
	}
	scan, err := os.ReadFile(fixture(set, "smartctl_scan", "stdout"))
	if err != nil {
		t.Fatalf("read scan capture: %v", err)
	}
	for _, line := range strings.Split(string(scan), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		captures["smartctl --json -i -H -A -l selftest "+fields[0]] =
			"smartctl_" + path.Base(fields[0])
	}
	return captures
}

func disksOf(t *testing.T, set string) []metrics.Disk {
	t.Helper()
	a := NewAdapter(&replayRunner{t: t, set: set, captures: diskCaptures(t, set)})
	disks, err := a.Disks(context.Background())
	if err != nil {
		t.Fatalf("Disks on %s: %v", set, err)
	}
	return disks
}

func diskNamed(t *testing.T, disks []metrics.Disk, name string) metrics.Disk {
	t.Helper()
	for _, disk := range disks {
		if disk.Device == name {
			return disk
		}
	}
	t.Fatalf("no disk named %q in %v", name, deviceNames(disks))
	return metrics.Disk{}
}

func deviceNames(disks []metrics.Disk) []string {
	names := make([]string, 0, len(disks))
	for _, disk := range disks {
		names = append(names, disk.Device)
	}
	return names
}

// The scan lists eight entries for five disks. What the extra three answer
// with -- valid JSON carrying no verdict -- is the only thing that tells them
// apart from a disk, which is why the list is read rather than filtered by
// name (JD-032).
func TestDisksReportsOnlyWhatAnswersLikeADisk(t *testing.T) {
	got := deviceNames(disksOf(t, "isengard"))
	want := []string{"ada0", "ada1", "ada2", "ada3", "nda0"}
	if len(got) != len(want) {
		t.Fatalf("disks = %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("disk %d = %q, want %q", i, got[i], name)
		}
	}
}

// The case nothing joins by name: ZFS stands on nda0, smartctl refuses
// /dev/nda0 and answers only as /dev/nvme0, and the serial is what says they
// are one disk. Reported under the kernel's name, with the path smartctl
// needs carried alongside it.
func TestDisksNamesTheNVMeTheWayTheKernelDoes(t *testing.T) {
	nvme := diskNamed(t, disksOf(t, "isengard"), "nda0")

	if nvme.Kind != metrics.DiskNVMe {
		t.Errorf("kind = %q, want %q", nvme.Kind, metrics.DiskNVMe)
	}
	if nvme.SmartPath != "/dev/nvme0" {
		t.Errorf("smart path = %q, want /dev/nvme0", nvme.SmartPath)
	}
	if nvme.Pool != "zroot" || nvme.Vdev != "nda0p4" {
		t.Errorf("pool/vdev = %q/%q, want zroot/nda0p4", nvme.Pool, nvme.Vdev)
	}
	if nvme.NVMe == nil {
		t.Fatal("an NVMe reported no NVMe health block")
	}
	if nvme.NVMe.AvailableSpare != 100 || nvme.NVMe.SpareThreshold != 1 {
		t.Errorf("spare = %d%% against a %d%% threshold, want 100 and 1",
			nvme.NVMe.AvailableSpare, nvme.NVMe.SpareThreshold)
	}
	// An NVMe has no attribute table, and inventing one would be the whole
	// mistake JD-032 refuses.
	if len(nvme.Failing) != 0 || len(nvme.Failed) != 0 {
		t.Errorf("NVMe reported ATA attributes: failing %v, failed %v",
			nvme.Failing, nvme.Failed)
	}
	if nvme.SelfTest != nil {
		t.Errorf("NVMe reported an ATA self-test log: %+v", nvme.SelfTest)
	}
}

// ada3 is the disk this whole issue came from: smartd has been mailing about
// it for days. The drive's own verdict is PASSED while attribute 184 is below
// threshold right now, and reporting either one alone would be a lie by
// omission (JD-032).
func TestDisksReportsAFailingAttributeOnADriveThatSaysItPassed(t *testing.T) {
	ada3 := diskNamed(t, disksOf(t, "isengard"), "ada3")

	if !ada3.Passed {
		t.Error("ada3 reported not passed; smartctl says PASSED")
	}
	if len(ada3.Failing) != 1 {
		t.Fatalf("failing = %+v, want exactly attribute 184", ada3.Failing)
	}
	failing := ada3.Failing[0]
	if failing.ID != 184 || failing.Name != "End-to-End_Error" {
		t.Errorf("failing = %d %q, want 184 End-to-End_Error", failing.ID, failing.Name)
	}
	if failing.Value != 91 || failing.Threshold != 99 {
		t.Errorf("failing value/threshold = %d/%d, want 91/99", failing.Value, failing.Threshold)
	}
	// Raw as the string smartctl printed. Attribute 190 on this same drive
	// prints "30 (8 71 32 26 0)", which is not a number at all.
	if failing.Raw != "9" {
		t.Errorf("raw = %q, want %q", failing.Raw, "9")
	}
	if len(ada3.Failed) != 1 || ada3.Failed[0].ID != 190 {
		t.Fatalf("failed = %+v, want exactly attribute 190", ada3.Failed)
	}
	if raw := ada3.Failed[0].Raw; raw != "30 (8 71 32 26 0)" {
		t.Errorf("attribute 190 raw = %q, want the string smartctl printed", raw)
	}

	if ada3.Model != "ST500LM000-1EJ162" {
		t.Errorf("model = %q", ada3.Model)
	}
	if ada3.PowerOnHours == nil || *ada3.PowerOnHours != 19257 {
		t.Errorf("power-on hours = %v, want 19257", ada3.PowerOnHours)
	}
	if ada3.TemperatureC == nil || *ada3.TemperatureC != 30 {
		t.Errorf("temperature = %v, want 30", ada3.TemperatureC)
	}
	if ada3.SelfTest == nil || !ada3.SelfTest.Passed {
		t.Fatalf("self-test = %+v, want a passing one", ada3.SelfTest)
	}
	if ada3.SelfTest.Hours != 19257 {
		t.Errorf("self-test hours = %d, want 19257", ada3.SelfTest.Hours)
	}
	// Read, never started (JD-032).
	if ada3.Pool != "backup" || ada3.Vdev != "gpt/backup1" {
		t.Errorf("pool/vdev = %q/%q, want backup/gpt/backup1", ada3.Pool, ada3.Vdev)
	}
}

// ada3 exits 32 -- "an attribute has been below threshold in the past" -- on a
// completely successful read. A reader that treated a non-zero exit as failure
// would report nothing about the one disk that matters.
func TestDisksReadsADriveThatExitsNonZero(t *testing.T) {
	result, err := replay(t, "isengard", "smartctl_ada3")
	if err == nil || result.ExitCode != 32 {
		t.Fatalf("the ada3 capture is meant to be a non-zero exit: exit %d, err %v",
			result.ExitCode, err)
	}
	if ada3 := diskNamed(t, disksOf(t, "isengard"), "ada3"); ada3.Model == "" {
		t.Error("the disk that exits 32 was reported with nothing in it")
	}
}

// Both pool naming styles, on one host: backup names its members by GEOM
// label and tank names them by partition. Neither is a special case.
func TestDisksJoinsPoolsNamedByLabelAndByPartition(t *testing.T) {
	disks := disksOf(t, "isengard")
	for _, tt := range []struct{ device, pool, vdev string }{
		{"ada0", "tank", "ada0p4"},        // by partition
		{"ada1", "tank", "ada1p3"},        // by partition
		{"ada2", "backup", "gpt/backup0"}, // by label
		{"ada3", "backup", "gpt/backup1"}, // by label
		{"nda0", "zroot", "nda0p4"},       // by partition, under a serial join
	} {
		disk := diskNamed(t, disks, tt.device)
		if disk.Pool != tt.pool || disk.Vdev != tt.vdev {
			t.Errorf("%s = %q/%q, want %q/%q",
				tt.device, disk.Pool, disk.Vdev, tt.pool, tt.vdev)
		}
	}
}

// The other half of the rule. rohan's da0 is a bare drive in a USB enclosure:
// geom reports the enclosure's serial and smartctl the drive's own, two
// different strings for one disk. The names already agree, so the name wins
// and a serial-first rule would have lost the disk.
func TestDisksMatchesByNameWhenTheSerialsDisagree(t *testing.T) {
	disks := disksOf(t, "rohan")
	if names := deviceNames(disks); len(names) != 2 {
		t.Fatalf("disks = %v, want ada0 and da0", names)
	}

	da0 := diskNamed(t, disks, "da0")
	if da0.Pool != "media" || da0.Vdev != "gpt/media" {
		t.Errorf("da0 = %q/%q, want media/gpt/media", da0.Pool, da0.Vdev)
	}
	if da0.SmartPath != "" {
		t.Errorf("smart path = %q; da0 is readable under its own name", da0.SmartPath)
	}
	// The two sources really do disagree here. If a recapture ever makes them
	// agree, this test stops proving what it claims to.
	inventory := NewAdapter(&replayRunner{t: t, set: "rohan",
		captures: diskCaptures(t, "rohan")}).diskInventory(context.Background())
	for _, entry := range inventory {
		if entry.name == "da0" && entry.ident == da0.Serial {
			t.Errorf("geom and smartctl now report the same serial %q for da0; "+
				"this test no longer exercises a name-over-serial match", entry.ident)
		}
	}

	ada0 := diskNamed(t, disks, "ada0")
	if ada0.Pool != "zroot" || ada0.Vdev != "ada0p3" {
		t.Errorf("ada0 = %q/%q, want zroot/ada0p3", ada0.Pool, ada0.Vdev)
	}
	// rohan's zroot disk is an SSD, which says so by reporting zero.
	if ada0.RotationRate == nil || *ada0.RotationRate != 0 {
		t.Errorf("rotation rate = %v, want 0 for an SSD", ada0.RotationRate)
	}
}

// A disk that no pool stands on is still a disk that can fail, so it is still
// reported -- with no pool rather than left out.
//
// Constructed, not captured: every disk on isengard is in a pool, and making
// one that is not would mean repartitioning a live host. Only the pool tree is
// replaced; the five health reads are the real ones.
func TestDisksReportsADiskWithNoPool(t *testing.T) {
	runner := &overrideRunner{
		inner: &replayRunner{t: t, set: "isengard", captures: diskCaptures(t, "isengard")},
		stdout: map[string]string{
			"zpool list -v -H -o name": "tank\n\tmirror-0\n\tada0p4\n\tada1p3\n",
		},
	}
	disks, err := NewAdapter(runner).Disks(context.Background())
	if err != nil {
		t.Fatalf("Disks: %v", err)
	}
	if len(disks) != 5 {
		t.Fatalf("disks = %v, want all five reported", deviceNames(disks))
	}

	pooled := map[string]bool{"ada0": true, "ada1": true}
	for _, disk := range disks {
		if pooled[disk.Device] {
			if disk.Pool != "tank" {
				t.Errorf("%s = pool %q, want tank", disk.Device, disk.Pool)
			}
			continue
		}
		if disk.Pool != "" || disk.Vdev != "" {
			t.Errorf("%s = pool %q/%q, want no pool", disk.Device, disk.Pool, disk.Vdev)
		}
		if disk.Model == "" {
			t.Errorf("%s was reported with no health in it", disk.Device)
		}
	}
}

// smartmontools is a package, not base. A host without it has no disks to
// report, which is an answer rather than a failed request (JD-021, JD-032).
func TestDisksWithoutSmartctlIsEmptyAndNotAnError(t *testing.T) {
	disks, err := NewAdapter(&missingBinaryRunner{missing: "smartctl"}).
		Disks(context.Background())
	if err != nil {
		t.Fatalf("a host without smartctl should not fail the report: %v", err)
	}
	if len(disks) != 0 {
		t.Fatalf("disks = %v, want none", deviceNames(disks))
	}
	// nil marshals to null, and a consumer reads that as a broken field
	// rather than an empty list.
	if disks == nil {
		t.Error("disks is nil; it should be an empty slice")
	}
}

// geom is base and always there, but if it ever is not, the names smartctl
// answered under are used as they are. That is right for every disk whose two
// names agree, and costs the NVMe its pool -- a gap, reported as a gap.
func TestDisksWithoutGeomFallsBackToSmartctlNames(t *testing.T) {
	runner := &overrideRunner{
		inner: &replayRunner{t: t, set: "isengard", captures: diskCaptures(t, "isengard")},
		fail:  map[string]bool{"geom disk list": true},
	}
	disks, err := NewAdapter(runner).Disks(context.Background())
	if err != nil {
		t.Fatalf("Disks: %v", err)
	}
	nvme := diskNamed(t, disks, "nvme0")
	if nvme.Pool != "" {
		t.Errorf("pool = %q; without geom there is nothing to join nvme0 to nda0", nvme.Pool)
	}
	// The disks whose names already agree are unaffected.
	if ada3 := diskNamed(t, disks, "ada3"); ada3.Pool != "backup" {
		t.Errorf("ada3 pool = %q, want backup", ada3.Pool)
	}
}

// The scan lists isengard's NVMe twice, as /dev/nda0 -d scsi and /dev/nvme0
// -d nvme. On this host only one of the two answers, so the capture alone
// cannot show one disk being reported once. Constructed: make both answer,
// with the same drive's real report, and the serial has to collapse them.
func TestDisksReportsOneDiskWhenTwoDriversBothAnswer(t *testing.T) {
	report, err := os.ReadFile(fixture("isengard", "smartctl_nvme0", "stdout"))
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	runner := &overrideRunner{
		inner: &replayRunner{t: t, set: "isengard", captures: diskCaptures(t, "isengard")},
		stdout: map[string]string{
			"smartctl --json -i -H -A -l selftest /dev/nda0": string(report),
		},
	}
	disks, err := NewAdapter(runner).Disks(context.Background())
	if err != nil {
		t.Fatalf("Disks: %v", err)
	}
	if names := deviceNames(disks); len(names) != 5 {
		t.Fatalf("disks = %v, want the NVMe reported once", names)
	}
	nvme := diskNamed(t, disks, "nda0")
	// The first of the two to answer wins, and /dev/nda0 is its own name, so
	// there is no path to carry.
	if nvme.SmartPath != "" {
		t.Errorf("smart path = %q, want none when nda0 answered for itself", nvme.SmartPath)
	}
}

// overrideRunner replaces or fails individual commands and replays the rest.
type overrideRunner struct {
	inner  *replayRunner
	stdout map[string]string
	fail   map[string]bool
}

func (r *overrideRunner) Run(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
	line := cmd.String()
	if r.fail[line] {
		return system.CommandResult{ExitCode: 127}, errors.New("not found")
	}
	if out, ok := r.stdout[line]; ok {
		return system.CommandResult{Stdout: out}, nil
	}
	return r.inner.Run(ctx, cmd)
}

// missingBinaryRunner is a host where one command is not installed. exec
// reports that before the process starts, so there is no exit status.
type missingBinaryRunner struct{ missing string }

func (r *missingBinaryRunner) Run(_ context.Context, cmd system.Command) (system.CommandResult, error) {
	if cmd.Name == r.missing {
		return system.CommandResult{ExitCode: -1},
			errors.New("exec: \"" + cmd.Name + "\": executable file not found in $PATH")
	}
	return system.CommandResult{}, nil
}

var (
	_ system.CommandRunner = (*overrideRunner)(nil)
	_ system.CommandRunner = (*missingBinaryRunner)(nil)
)

func TestResolveName(t *testing.T) {
	inventory := []geomDisk{
		{name: "ada0", ident: "S2Z8J5BD600007"},
		{name: "nda0", ident: "P300MDCB251225001124"},
		{name: "da0", ident: "2HC015KJ"}, // the USB enclosure's serial
	}
	for _, tt := range []struct {
		why, smartName, serial, want string
	}{
		{"a name that matches is the answer", "ada0", "S2Z8J5BD600007", "ada0"},
		{"the NVMe, which matches on serial alone", "nvme0", "P300MDCB251225001124", "nda0"},
		{"a name match outranks a serial that disagrees", "da0", "5VP6Q01R", "da0"},
		// Constructed: neither host has a disk whose smartctl serial is
		// another disk's geom ident, so only this says which lookup wins.
		// The name does: smartctl opened /dev/da0 and the kernel has a da0,
		// which is identity rather than a matching string.
		{"a name match outranks a serial pointing elsewhere", "da0",
			"S2Z8J5BD600007", "da0"},
		{"neither matches: smartctl's name stands", "ada9", "NOPE", "ada9"},
		{"no serial to match on", "nvme0", "", "nvme0"},
		{"no inventory at all", "nvme0", "P300MDCB251225001124", "nvme0"},
	} {
		have := inventory
		if tt.why == "no inventory at all" {
			have = nil
		}
		if got := resolveName(tt.smartName, tt.serial, have); got != tt.want {
			t.Errorf("%s: resolveName(%q, %q) = %q, want %q",
				tt.why, tt.smartName, tt.serial, got, tt.want)
		}
	}
}

// A drive whose ident geom does not know must not swallow every disk that
// reports no serial either.
func TestResolveNameIgnoresAnEmptyIdent(t *testing.T) {
	inventory := []geomDisk{{name: "cd0", ident: ""}, {name: "ada0", ident: "AAAA"}}
	if got := resolveName("ses0", "", inventory); got != "ses0" {
		t.Errorf("resolveName with an empty serial = %q, want ses0", got)
	}
}

func TestResolveDevice(t *testing.T) {
	labels := map[string]string{"gpt/backup1": "ada3p1", "gpt/media": "da0p1"}
	for _, tt := range []struct{ vdev, want string }{
		{"ada0p4", "ada0"},
		{"nda0p4", "nda0"}, // the digit in the name must survive
		{"ada0s1a", "ada0"},
		{"gpt/backup1", "ada3"},
		{"gpt/media", "da0"},
		{"/dev/ada1p3", "ada1"},
		{"ada2", "ada2"}, // a whole disk, no partition
		{"mirror-0", "mirror-0"},
		{"gpt/unlabelled", ""}, // a label glabel does not know
		{"/var/tmp/sparsefile", ""},
	} {
		if got := resolveDevice(tt.vdev, labels); got != tt.want {
			t.Errorf("resolveDevice(%q) = %q, want %q", tt.vdev, got, tt.want)
		}
	}
}
