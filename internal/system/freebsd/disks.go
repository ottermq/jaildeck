package freebsd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/metrics"
	"github.com/ottermq/jaildeck/internal/system"
)

// smartctlTimeout bounds one device's health read. Each is a handful of ATA
// or NVMe commands and returns in milliseconds on a healthy drive; a drive
// that is not healthy is exactly the one worth waiting for (JD-014).
const smartctlTimeout = 30 * time.Second

// vdevPartitionRE strips a partition or slice from a provider name:
// ada3p1 -> ada3, ada0s1a -> ada0. Anchored because only a TRAILING suffix is
// a partition; anywhere else those letters are part of the device name. No
// FreeBSD disk name in these fixtures tells the two apart, so the anchor is
// precision rather than a fix for an observed bug.
var vdevPartitionRE = regexp.MustCompile(`(p\d+|s\d+[a-h]?)$`)

// Disks reports every device smartctl can read, with the pool it belongs to.
//
// Four reads, in order: the scan list, the host's own disk inventory, the
// pools' vdev trees, and then one call per scanned device. The scan list is
// deliberately not filtered first -- it is not a list of disks, and what each
// entry answers with is the only reliable way to tell a disk from a DVD
// drive, an enclosure, or the same NVMe listed twice under two drivers
// (JD-032).
func (a *Adapter) Disks(ctx context.Context) ([]metrics.Disk, error) {
	devices, err := a.scanDevices(ctx)
	if err != nil {
		// smartmontools is a package, not base, and it lives in
		// /usr/local/sbin, which is not on the PATH rc gives a service. Either
		// way the report gets the reason rather than an empty list that reads
		// like a host with no disks (JD-032) -- the empty list is what hid
		// disk health from the packaged service for two releases.
		if errors.Is(err, system.ErrCommandNotFound) {
			return nil, fmt.Errorf("%w: %v", metrics.ErrSmartctlUnavailable, err)
		}
		return nil, err
	}

	inventory := a.diskInventory(ctx)
	members, err := a.poolMembers(ctx)
	if err != nil {
		// The health is still worth reporting without the join; which pool a
		// disk belongs to is the better half of the answer, not the whole of
		// it.
		members = map[string]poolMember{}
	}

	reads := a.readDisks(ctx, devices)
	disks := make([]metrics.Disk, 0, len(devices))
	seen := map[string]bool{}
	for i, device := range devices {
		report, ok, err := reads[i].report, reads[i].ok, reads[i].err
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		// The name the kernel uses, which is rarely but importantly not the
		// path smartctl had to be given.
		name := resolveName(deviceName(report.Device.Name, device),
			report.SerialNumber, inventory)
		if seen[name] {
			// The same disk this scan already listed under another driver.
			continue
		}
		seen[name] = true

		disk := report.disk(name)
		if path := strings.TrimPrefix(device, "/dev/"); path != name {
			// Say so, because an operator told "nda0 is failing" who reaches
			// for smartctl will be refused by it.
			disk.SmartPath = device
		}
		if member, inPool := members[name]; inPool {
			disk.Pool, disk.Vdev = member.pool, member.vdev
		}
		disks = append(disks, disk)
	}
	return disks, nil
}

// smartctlParallel is how many disks are read at once. smartctl on one disk
// took up to 0.6 s on isengard while a hard disk answered, and reading five in
// turn made /api/metrics take 1.5 s (#135); the disks are independent.
const smartctlParallel = 4

// diskRead is one device's readDisk answer.
type diskRead struct {
	report smartReport
	ok     bool
	err    error
}

// readDisks reads every device, smartctlParallel at a time, and answers in
// the devices' own order, so that what Disks makes of them -- which name
// wins for a disk listed twice -- is the same as reading them in turn.
func (a *Adapter) readDisks(ctx context.Context, devices []string) []diskRead {
	reads := make([]diskRead, len(devices))
	slots := make(chan struct{}, smartctlParallel)
	var wg sync.WaitGroup
	for i, device := range devices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			report, ok, err := a.readDisk(ctx, device)
			reads[i] = diskRead{report: report, ok: ok, err: err}
		}()
	}
	wg.Wait()
	return reads
}

// deviceName is the bare name smartctl answered under, falling back to the
// path it was asked about.
func deviceName(reported, asked string) string {
	return strings.TrimPrefix(firstNonEmpty(reported, asked), "/dev/")
}

// scanDevices asks smartctl which devices exist. Its own list, not a guess
// from /dev: smartctl knows which driver each one needs.
func (a *Adapter) scanDevices(ctx context.Context) ([]string, error) {
	result, err := a.runHost(ctx, system.Command{
		Name:    "smartctl",
		Args:    []string{"--scan"},
		Timeout: smartctlTimeout,
	})
	if err != nil {
		return nil, err
	}

	devices := make([]string, 0)
	complaint := ""
	for _, line := range nonEmptyLines(result.Stdout) {
		// "/dev/ada0 -d atacam # /dev/ada0, ATA device"
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/") {
			devices = append(devices, fields[0])
			continue
		}
		// Measured: run without privilege, the scan prints
		// "# scan_smart_devices: Permission denied" and exits 0. A scan that
		// found nothing and said something is that, not an empty host.
		if strings.HasPrefix(strings.TrimSpace(line), "#") && complaint == "" {
			complaint = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		}
	}
	if len(devices) == 0 && complaint != "" {
		return nil, fmt.Errorf("smartctl --scan: %s", complaint)
	}
	return devices, nil
}

// readDisk reads one device. The second return is false when the device is
// not a disk smartctl has anything to say about.
//
// Non-zero exit is expected and carries information rather than failure:
// isengard's ada3 exits 32 -- "an attribute has been below threshold in the
// past" -- on a completely successful read, and a reader that treated that
// as an error would report nothing about the one disk that matters. So the
// output is parsed whatever the exit status, and the absence of smart_status
// is what says "not a disk" (JD-032).
func (a *Adapter) readDisk(ctx context.Context, device string) (smartReport, bool, error) {
	cmd := system.Command{
		Name: "smartctl",
		Args: []string{"--json", "-i", "-H", "-A", "-l", "selftest", device},
		// A failing drive can be slow and verbose; the cap is generous
		// because the alternative is losing the report that explains it.
		Timeout:   smartctlTimeout,
		MaxOutput: maxSmartOutput,
	}
	result, runErr := a.runner.Run(ctx, cmd)
	if strings.TrimSpace(result.Stdout) == "" {
		// Nothing to parse. A device that is not there, or a smartctl that
		// could not start at all.
		if runErr != nil {
			return smartReport{}, false, nil
		}
		return smartReport{}, false, nil
	}

	var report smartReport
	if err := json.Unmarshal([]byte(result.Stdout), &report); err != nil {
		return smartReport{}, false, fmt.Errorf("parse smartctl --json for %s: %w", device, err)
	}
	if report.SmartStatus == nil {
		// A DVD drive, an enclosure, or a device that could not be opened
		// the way --scan suggested. Each of those answers with valid JSON and
		// no verdict in it.
		return smartReport{}, false, nil
	}
	return report, true, nil
}

// maxSmartOutput bounds one device's JSON. isengard's most talkative drive
// produces under 15 KB with the self-test log included; this is room for a
// drive with a full error log and no more.
const maxSmartOutput = 1 << 20

// smartReport is the half of smartctl's JSON this reads. Deliberately
// partial: the document carries a hundred keys and reading the ones that
// are not understood would invite reporting them.
type smartReport struct {
	Device struct {
		Name string `json:"name"`
	} `json:"device"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	UserCapacity struct {
		Bytes int64 `json:"bytes"`
	} `json:"user_capacity"`
	RotationRate *int `json:"rotation_rate"`
	SmartStatus  *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature *struct {
		Current *int `json:"current"`
	} `json:"temperature"`
	PowerOnTime *struct {
		Hours *int64 `json:"hours"`
	} `json:"power_on_time"`

	ATAAttributes *struct {
		Table []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Value      int    `json:"value"`
			Worst      int    `json:"worst"`
			Thresh     int    `json:"thresh"`
			WhenFailed string `json:"when_failed"`
			Raw        struct {
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`

	ATASelfTest *struct {
		Standard struct {
			Table []struct {
				Type struct {
					String string `json:"string"`
				} `json:"type"`
				Status struct {
					String string `json:"string"`
					Passed *bool  `json:"passed"`
				} `json:"status"`
				LifetimeHours int64 `json:"lifetime_hours"`
			} `json:"table"`
		} `json:"standard"`
	} `json:"ata_smart_self_test_log"`

	NVMeHealth *struct {
		CriticalWarning int   `json:"critical_warning"`
		PercentageUsed  int   `json:"percentage_used"`
		AvailableSpare  int   `json:"available_spare"`
		SpareThreshold  int   `json:"available_spare_threshold"`
		MediaErrors     int64 `json:"media_errors"`
		ErrorLogEntries int64 `json:"num_err_log_entries"`
	} `json:"nvme_smart_health_information_log"`
}

// disk turns one report into the shape the domain reports.
//
// when_failed is smartctl's own judgment and is carried as given rather than
// re-derived from value against threshold: deriving it would be a second
// opinion that can disagree with the first (JD-032).
func (r smartReport) disk(name string) metrics.Disk {
	disk := metrics.Disk{
		Device:       name,
		Kind:         metrics.DiskATA,
		Model:        r.ModelName,
		Serial:       r.SerialNumber,
		Capacity:     r.UserCapacity.Bytes,
		RotationRate: r.RotationRate,
		Passed:       r.SmartStatus.Passed,
	}
	if r.Temperature != nil {
		disk.TemperatureC = r.Temperature.Current
	}
	if r.PowerOnTime != nil {
		disk.PowerOnHours = r.PowerOnTime.Hours
	}

	if r.NVMeHealth != nil {
		// An NVMe has no attribute table at all. Reporting one as though it
		// had ATA attributes would be inventing data (JD-032).
		disk.Kind = metrics.DiskNVMe
		disk.NVMe = &metrics.NVMeHealth{
			CriticalWarning: r.NVMeHealth.CriticalWarning,
			PercentageUsed:  r.NVMeHealth.PercentageUsed,
			AvailableSpare:  r.NVMeHealth.AvailableSpare,
			SpareThreshold:  r.NVMeHealth.SpareThreshold,
			MediaErrors:     r.NVMeHealth.MediaErrors,
			ErrorLogEntries: r.NVMeHealth.ErrorLogEntries,
		}
	}

	if r.ATAAttributes != nil {
		for _, row := range r.ATAAttributes.Table {
			attribute := metrics.Attribute{
				ID: row.ID, Name: row.Name, Value: row.Value,
				Worst: row.Worst, Threshold: row.Thresh,
				// The string smartctl printed, never a number to reason
				// about: vendors encode several fields into that integer.
				Raw: row.Raw.String,
			}
			switch row.WhenFailed {
			case "now":
				disk.Failing = append(disk.Failing, attribute)
			case "past":
				disk.Failed = append(disk.Failed, attribute)
			}
		}
	}

	if r.ATASelfTest != nil && len(r.ATASelfTest.Standard.Table) > 0 {
		// The newest entry. A self-test log is ordered newest first, and
		// "the drive scanned itself and found nothing" is only interesting
		// about the most recent one.
		newest := r.ATASelfTest.Standard.Table[0]
		passed := newest.Status.Passed != nil && *newest.Status.Passed
		disk.SelfTest = &metrics.SelfTest{
			Type:   newest.Type.String,
			Status: newest.Status.String,
			Passed: passed,
			Hours:  newest.LifetimeHours,
		}
	}
	return disk
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// poolMember is which pool a device belongs to, and the name that pool knows
// it by -- gpt/backup1 rather than ada3, because that is what `zpool status`
// prints and therefore what an operator reading it is looking for.
type poolMember struct {
	pool string
	vdev string
}

// poolMembers maps a device name to the pool standing on it.
//
// Two reads: the pools' vdev trees, and the GEOM labels needed to resolve
// the ones named by label. A pool names its members however it was created
// -- isengard's `backup` says gpt/backup1 and its `tank` says ada0p4 -- so
// both paths are ordinary rather than one being a special case.
func (a *Adapter) poolMembers(ctx context.Context) (map[string]poolMember, error) {
	result, err := a.runHost(ctx, system.Command{
		Name:    "zpool",
		Args:    []string{"list", "-v", "-H", "-o", "name"},
		Timeout: metricsTimeout,
	})
	if err != nil {
		// A host without ZFS: zpool exits non-zero and there are no pools,
		// which is an answer (JD-021).
		if exited(result, err) {
			return map[string]poolMember{}, nil
		}
		return nil, err
	}

	labels := a.geomLabels(ctx)
	members := map[string]poolMember{}
	pool := ""
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			// A pool's own name sits at the left margin; everything beneath
			// it is indented.
			pool = strings.TrimSpace(line)
			continue
		}
		vdev := strings.TrimSpace(line)
		device := resolveDevice(vdev, labels)
		if device == "" || pool == "" {
			continue
		}
		// mirror-0, indirect-1 and the rest resolve to nothing, and are
		// ignored without having to enumerate what a vdev type can be
		// called -- the join only keeps what matches a real device.
		members[device] = poolMember{pool: pool, vdev: vdev}
	}
	return members, nil
}

// geomLabels maps a GEOM label to the provider under it: gpt/backup1 to
// ada3p1. Absent or unreadable, a pool's labelled members simply go
// unjoined, which costs the pool name and nothing else.
func (a *Adapter) geomLabels(ctx context.Context) map[string]string {
	result, err := a.runHost(ctx, system.Command{
		Name:    "glabel",
		Args:    []string{"status"},
		Timeout: metricsTimeout,
	})
	if err != nil {
		return map[string]string{}
	}

	labels := map[string]string{}
	for _, line := range nonEmptyLines(result.Stdout) {
		// "     gpt/backup1     N/A  ada3p1", with a header line above.
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] == "Name" {
			continue
		}
		labels[fields[0]] = fields[2]
	}
	return labels
}

// resolveDevice turns a vdev name into the device beneath it, or "" when it
// is not a device at all.
func resolveDevice(vdev string, labels map[string]string) string {
	provider := strings.TrimPrefix(vdev, "/dev/")
	if under, labelled := labels[provider]; labelled {
		provider = under
	}
	if strings.Contains(provider, "/") {
		// A label with no entry in glabel status, or a file-backed vdev.
		// Neither is a disk this can report on.
		return ""
	}
	return vdevPartitionRE.ReplaceAllString(provider, "")
}

// geomDisk is one entry of the host's own disk inventory: the name the kernel
// uses, and the serial the drive reports through it.
type geomDisk struct {
	name  string
	ident string
}

// diskInventory reads `geom disk list`: every disk the kernel has, by the name
// everything else on the host -- zpool, gpart, the operator -- calls it.
//
// Base system and unprivileged, unlike smartctl, so this is the cheaper and
// more authoritative of the two lists. It is not used to decide WHICH disks to
// report, though: geom knows a disk exists and nothing about its health, and a
// device it does not list can still answer smartctl.
func (a *Adapter) diskInventory(ctx context.Context) []geomDisk {
	result, err := a.runHost(ctx, system.Command{
		Name:    "geom",
		Args:    []string{"disk", "list"},
		Timeout: metricsTimeout,
	})
	if err != nil {
		// Without it the names smartctl answered under are used as they are,
		// which is right for every disk whose two names already agree.
		return nil
	}

	inventory := make([]geomDisk, 0)
	for _, line := range nonEmptyLines(result.Stdout) {
		if name, found := strings.CutPrefix(line, "Geom name:"); found {
			inventory = append(inventory, geomDisk{name: strings.TrimSpace(name)})
			continue
		}
		ident, found := strings.CutPrefix(strings.TrimSpace(line), "ident:")
		if !found || len(inventory) == 0 {
			continue
		}
		inventory[len(inventory)-1].ident = strings.TrimSpace(ident)
	}
	return inventory
}

// resolveName picks the name to report a disk under: the one the kernel uses,
// which is not always the path smartctl had to be given.
//
// By name first, by serial second, and both halves are load-bearing.
//
// By name, because rohan's da0 is a bare drive in a USB enclosure: geom reports
// the enclosure's serial and smartctl the drive's own, two different strings
// for one disk. A serial-first rule would fail to place a disk whose name
// already matched perfectly.
//
// By serial, because isengard's zroot stands on nda0 and smartctl refuses
// /dev/nda0 outright -- "NVMe controller controller/namespace ids must begin
// with '/dev/nvme'" -- answering only as /dev/nvme0. Nothing joins nvme0 to
// nda0 by name, and guessing that nvmeN is ndaN would be a convention rather
// than a fact. The serial is the fact: geom reports the same one (JD-032).
func resolveName(smartName, serial string, inventory []geomDisk) string {
	for _, disk := range inventory {
		if disk.name == smartName {
			return smartName
		}
	}
	if serial != "" {
		for _, disk := range inventory {
			if disk.ident == serial {
				return disk.name
			}
		}
	}
	return smartName
}

var _ metrics.DiskSource = (*Adapter)(nil)
