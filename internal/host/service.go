package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

const (
	racctOID = "kern.racct.enable"
	archOID  = "hw.machine_arch"

	// execTimeoutSetting is checked for presence only. Its value belongs to the
	// host (JD-014); the remedy suggests the one that chain is built around.
	execTimeoutSetting = "exec.timeout"
	jailConf           = "/etc/jail.conf"
)

type Service struct {
	system          HostSystem
	operationLogger audit.Logger
	// pool is JAILDECK_ZFS_POOL. Empty means discover, which succeeds only
	// when exactly one pool exists.
	pool string
	// ensureMu serialises ensures, so two concurrent PUTs cannot both see a
	// dataset missing and the second fail creating what the first just made.
	ensureMu sync.Mutex
}

func NewService(system HostSystem, operationLogger audit.Logger, pool string) *Service {
	return &Service{system: system, operationLogger: operationLogger, pool: pool}
}

// verdict is a check's judgment before the report gives it an id, a title and
// what it gates.
type verdict struct {
	status   CheckStatus
	observed string
	detail   string
	remedy   string
}

// Readiness observes the host and judges each check. Nothing in it writes: a
// check that cannot observe reports unknown rather than failing the request,
// so the only error is the caller going away.
func (s *Service) Readiness(ctx context.Context) (Report, error) {
	verdicts := map[CheckID]verdict{
		CheckRacct:             judgeRacct(s.system.Sysctl(ctx, racctOID)),
		CheckExecTimeout:       judgeExecTimeout(s.system.JailConfDefaults(ctx)),
		CheckBridges:           judgeBridges(s.system.Bridges(ctx)),
		CheckDevfsRulesets:     judgeDevfsRulesets(s.system.DevfsRulesets(ctx)),
		CheckCPUVirtualization: s.checkCPUVirtualization(ctx),
		CheckPF:                judgePF(s.system.PFStatus(ctx)),
	}
	pool, pools, poolVerdict, _ := s.choosePool(ctx)
	verdicts[CheckZFSPool] = poolVerdict
	if poolVerdict.status == CheckPass {
		verdicts[CheckZFSLayout] = judgeLayout(s.readLayout(ctx, pool, pools))
	} else {
		verdicts[CheckZFSLayout] = verdict{
			status: CheckUnknown,
			detail: "There is no pool to look for the layout on until the ZFS pool check passes.",
		}
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return buildReport(verdicts), nil
}

func buildReport(verdicts map[CheckID]verdict) Report {
	checks := make([]Check, 0, len(checkSpecs))
	for _, spec := range checkSpecs {
		v := verdicts[spec.id]
		checks = append(checks, Check{
			ID:       spec.id,
			Title:    spec.title,
			Status:   v.status,
			Observed: v.observed,
			Detail:   v.detail,
			Remedy:   v.remedy,
			Gates:    append(make([]CapabilityID, 0, len(spec.gates)), spec.gates...),
		})
	}

	capabilities := make([]Capability, 0, len(capabilitySpecs))
	for _, spec := range capabilitySpecs {
		gatedBy := make([]CheckID, 0)
		statuses := make([]CheckStatus, 0)
		for _, check := range checks {
			if slices.Contains(check.Gates, spec.id) {
				gatedBy = append(gatedBy, check.ID)
				statuses = append(statuses, check.Status)
			}
		}
		capabilities = append(capabilities, Capability{
			ID:      spec.id,
			Title:   spec.title,
			Status:  deriveCapability(statuses),
			GatedBy: gatedBy,
		})
	}
	return Report{Capabilities: capabilities, Checks: checks}
}

// deriveCapability is the one rule every client would otherwise reimplement:
// any failed gate makes a capability unavailable, any unknown one makes it
// unknown, and only then is it ready.
func deriveCapability(gates []CheckStatus) CapabilityStatus {
	switch {
	case slices.Contains(gates, CheckFail):
		return CapabilityUnavailable
	case slices.Contains(gates, CheckUnknown):
		return CapabilityUnknown
	default:
		return CapabilityReady
	}
}

// judgeRacct distinguishes three kernel states. "0" is fixed by a loader
// tunable; a kernel without the oid has no RACCT at all, where that same
// advice would be wrong, so it is unknown and carries no remedy.
func judgeRacct(value string, err error) verdict {
	if errors.Is(err, ErrUnknownOID) {
		return verdict{
			status: CheckUnknown,
			detail: "This kernel has no " + racctOID + " oid; rctl reports \"RACCT/RCTL support not present in kernel\". " +
				"No tunable enables it: the kernel itself would need RACCT and RCTL.",
		}
	}
	if err != nil {
		return unobserved(racctOID, err)
	}
	switch value {
	case "1":
		return verdict{status: CheckPass, observed: value, detail: "rctl rules are enforced."}
	case "0":
		return verdict{
			status:   CheckFail,
			observed: value,
			detail: "rctl rules are accepted but ignored while this is 0; rctl reports \"RACCT/RCTL present, but disabled\". " +
				"It is a loader tunable, so it takes effect at the next reboot.",
			remedy: "sysrc -f /boot/loader.conf " + racctOID + "=1",
		}
	default:
		return verdict{
			status:   CheckUnknown,
			observed: value,
			detail:   fmt.Sprintf("%s read %q, which is neither 0 nor 1.", racctOID, value),
		}
	}
}

// judgeExecTimeout passes on the setting's presence in the shared defaults.
// A missing jail.conf is a fail, not an unknown: jails run fine without one,
// but then no shared default exists for jail(8) to apply.
func judgeExecTimeout(defaults map[string]string, err error) verdict {
	if err != nil {
		return unobserved(jailConf, err)
	}
	if value, ok := defaults[execTimeoutSetting]; ok {
		return verdict{
			status:   CheckPass,
			observed: strings.TrimSpace(value),
			detail:   "jail(8) bounds each exec.* command itself, which Jail Deck's own timeout relies on (JD-014).",
		}
	}
	// rc.d/jail uses a jail.conf.d file on its own unless jail.conf includes
	// that directory, so a jail.conf created without the .include would set a
	// default that none of those jails ever reads.
	if defaults == nil {
		return verdict{
			status: CheckFail,
			detail: jailConf + " does not exist, so no jail has a shared exec.timeout and jail(8) leaves exec.* commands unbounded (JD-014). " +
				"The remedy creates it with the default and includes jail.conf.d, which rc.d/jail otherwise reads on its own; " +
				"setting exec.timeout in each jail.conf.d stanza works too.",
			remedy: `printf 'exec.timeout = 120;\n.include "/etc/jail.conf.d/*.conf";\n' >> ` + jailConf,
		}
	}
	return verdict{
		status: CheckFail,
		detail: jailConf + " sets no shared exec.timeout, so jail(8) leaves exec.* commands unbounded (JD-014). " +
			"rc.d/jail applies these defaults only to jails it reads through " + jailConf + ": a jail.conf.d file it does not .include is used on its own.",
		remedy: "echo 'exec.timeout = 120;' >> " + jailConf,
	}
}

func judgeBridges(bridges []string, err error) verdict {
	if err != nil {
		return unobserved("bridges", err)
	}
	if len(bridges) == 0 {
		return verdict{
			status: CheckFail,
			detail: "VNET jails join a bridge the operator owns (JD-017); Jail Deck never creates one. " +
				"Add a member with ifconfig bridge0 addm <interface> and persist it as ifconfig_bridge0 in /etc/rc.conf.",
			remedy: "ifconfig bridge0 create && sysrc cloned_interfaces+=bridge0",
		}
	}
	return verdict{
		status:   CheckPass,
		observed: strings.Join(bridges, ", "),
		detail:   "A VNET jail spec names one of these (JD-017).",
	}
}

// judgeDevfsRulesets trusts the kernel, not the files: every stock host has
// rulesets 1 to 5 loaded from /etc/defaults/devfs.rules with no
// /etc/devfs.rules at all.
func judgeDevfsRulesets(rulesets []DevfsRuleset, err error) verdict {
	if err != nil {
		return unobserved("devfs rulesets", err)
	}
	if len(rulesets) == 0 {
		return verdict{
			status: CheckFail,
			detail: "No devfs ruleset is loaded, so a jail spec has none to name (JD-018). " +
				"rc.d/devfs loads them from /etc/defaults/devfs.rules and /etc/devfs.rules.",
			remedy: "service devfs restart",
		}
	}
	loaded := make([]string, 0, len(rulesets))
	for _, rs := range rulesets {
		if rs.Name == "" {
			loaded = append(loaded, fmt.Sprint(rs.Number))
			continue
		}
		loaded = append(loaded, fmt.Sprintf("%s=%d", rs.Name, rs.Number))
	}
	return verdict{
		status:   CheckPass,
		observed: strings.Join(loaded, ", "),
		detail:   "A jail spec names one of these numbers as devfs_ruleset (JD-018).",
	}
}

func (s *Service) checkCPUVirtualization(ctx context.Context) verdict {
	// The VT-x line is an x86 thing. Elsewhere its absence is not ignorance
	// but the check not applying, and a fail would send the operator into a
	// BIOS that does not exist. An unreadable arch is no reason to give up:
	// a VT-x line, if there is one, settles it.
	arch, err := s.system.Sysctl(ctx, archOID)
	arch = strings.TrimSpace(arch)
	if err == nil && arch != "amd64" && arch != "i386" {
		return verdict{
			status:   CheckUnknown,
			observed: arch,
			detail:   "The VT-x check applies to x86 hosts only; bhyve on " + arch + " is not something this report can judge.",
		}
	}
	return judgeCPUVirtualization(s.system.CPUVirtualization(ctx))
}

// judgeCPUVirtualization never fails on a missing line. dmesg.boot can be
// absent and an AMD CPU prints a line this report does not yet recognise;
// sending the operator into the BIOS of a capable machine is worse than
// saying it does not know.
func judgeCPUVirtualization(cpu CPUVirtualization, err error) verdict {
	if err != nil {
		return unobserved("the boot messages", err)
	}
	if !cpu.Reported {
		return verdict{
			status: CheckUnknown,
			detail: "The boot messages carry no VT-x line. That is not evidence the CPU lacks it: the boot log may be gone, " +
				"and an AMD CPU reports SVM, which this report does not yet recognise.",
		}
	}
	observed := strings.Join(cpu.Features, ",")
	if cpu.DisabledInBIOS {
		return verdict{
			status:   CheckFail,
			observed: "(disabled in BIOS) " + observed,
			detail:   "The CPU has VT-x but the firmware has switched it off, so bhyve cannot start a guest (JD-019).",
			remedy:   "Enable Intel VT-x (Virtualization Technology) in the firmware setup, then reboot.",
		}
	}
	missing := make([]string, 0, 2)
	for _, want := range []string{"EPT", "UG"} {
		if !slices.Contains(cpu.Features, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return verdict{
			status:   CheckFail,
			observed: observed,
			detail:   "bhyve needs VT-x with EPT and UG (unrestricted guest); this CPU reports no " + strings.Join(missing, " or ") + " (JD-019).",
			remedy:   "No setting adds a CPU feature: bhyve needs a CPU with EPT and UG, or, inside a VM, nested virtualisation on its hypervisor.",
		}
	}
	return verdict{
		status:   CheckPass,
		observed: observed,
		detail:   "bhyve's hardware prerequisites are present (JD-019).",
	}
}

func judgePF(pf PFStatus, err error) verdict {
	if err != nil {
		return unobserved("PF", err)
	}
	const pfDetail = "The ruleset is the operator's, in /etc/pf.conf; Jail Deck only reads it (JD-020)."
	switch {
	case pf.Loaded && pf.Enabled:
		return verdict{status: CheckPass, observed: "Enabled", detail: pfDetail}
	case pf.Loaded:
		return verdict{
			status:   CheckFail,
			observed: "Disabled",
			detail:   "pf.ko is loaded but PF is disabled. " + pfDetail,
			remedy:   "sysrc pf_enable=YES && service pf start",
		}
	default:
		return verdict{
			status:   CheckFail,
			observed: "not loaded",
			detail:   "pf.ko is not loaded. service pf start loads it along with /etc/pf.conf, which must exist first. " + pfDetail,
			remedy:   "sysrc pf_enable=YES && service pf start",
		}
	}
}

// choosePool settles which pool holds the layout, as the zfs_pool verdict. It
// never picks one of several: the first pool printed can be a replication
// target whose copy of the layout is mounted nowhere. It also returns every
// pool it saw, and err, the observation error behind an unknown, for callers
// that must answer with a status code.
func (s *Service) choosePool(ctx context.Context) (string, []string, verdict, error) {
	pools, err := s.system.Pools(ctx)
	if err != nil {
		var cmdErr *system.CommandError
		if errors.As(err, &cmdErr) && cmdErr.Result.ExitCode > 0 {
			// zpool and zfs are in base everywhere, so no ZFS shows up as
			// zpool failing rather than as a missing binary.
			return "", nil, verdict{
				status:   CheckFail,
				observed: strings.TrimSpace(cmdErr.Result.Stderr),
				detail: "zpool list failed, so this host has no usable ZFS. Jail Deck requires it (JD-006): " +
					"load the module, persist it, then create a pool with zpool create.",
				remedy: "kldload zfs && sysrc -f /boot/loader.conf zfs_load=YES",
			}, nil
		}
		return "", nil, unobserved("the ZFS pools", err), err
	}
	listed := strings.Join(pools, ", ")
	if s.pool != "" {
		if slices.Contains(pools, s.pool) {
			return s.pool, pools, verdict{
				status:   CheckPass,
				observed: s.pool,
				detail:   "JAILDECK_ZFS_POOL names this pool.",
			}, nil
		}
		return "", pools, verdict{
			status:   CheckFail,
			observed: listed,
			detail:   fmt.Sprintf("JAILDECK_ZFS_POOL is %q, which is not among the pools zpool lists.", s.pool),
			remedy:   poolRemedy(pools),
		}, nil
	}
	switch len(pools) {
	case 0:
		return "", pools, verdict{
			status: CheckFail,
			detail: "ZFS is loaded but no pool is imported. Jail Deck requires one (JD-006).",
			remedy: "zpool create <pool> <device>",
		}, nil
	case 1:
		return pools[0], pools, verdict{
			status:   CheckPass,
			observed: pools[0],
			detail:   "The only pool on the host is adopted; set JAILDECK_ZFS_POOL to pin it.",
		}, nil
	default:
		return "", pools, verdict{
			status:   CheckFail,
			observed: listed,
			detail:   "There is more than one pool and JAILDECK_ZFS_POOL is not set. Jail Deck does not guess which one holds the jails.",
			remedy:   poolRemedy(pools),
		}, nil
	}
}

func poolRemedy(pools []string) string {
	if len(pools) == 0 {
		return "Set JAILDECK_ZFS_POOL in Jail Deck's environment once a pool exists, then restart it."
	}
	return "Set JAILDECK_ZFS_POOL in Jail Deck's environment to one of: " + strings.Join(pools, ", ") + ", then restart it."
}

// readLayout lists the pool once and matches the four datasets in Go. Naming
// them to zfs list instead would exit 1 on the first missing one and lose the
// rest; with -r the only failure is the pool itself being gone.
//
// A missing dataset would be mounted at its expected path, so when one is
// missing every pool is read: if any dataset already has that mountpoint,
// creating the missing one would mount over it, and over any jails inside.
func (s *Service) readLayout(ctx context.Context, pool string, pools []string) (Layout, error) {
	datasets, err := s.system.Datasets(ctx, pool)
	if err != nil {
		return Layout{}, err
	}
	mountpoints := make(map[string]string, len(datasets))
	for _, d := range datasets {
		mountpoints[d.Name] = d.Mountpoint
	}

	parent := pool + "/jails"
	want := []LayoutDataset{{Name: parent, ExpectedMountpoint: jailsMountpoint}}
	for _, child := range layoutChildren {
		want = append(want, LayoutDataset{Name: parent + "/" + child, ExpectedMountpoint: jailsMountpoint + "/" + child})
	}

	layout := Layout{Pool: pool, Datasets: make([]LayoutDataset, 0, len(want)), Missing: make([]string, 0)}
	for _, d := range want {
		// Full dataset paths only: a pool can itself be called "media".
		d.Mountpoint, d.Present = mountpoints[d.Name]
		if !d.Present {
			layout.Missing = append(layout.Missing, d.Name)
		}
		layout.Datasets = append(layout.Datasets, d)
	}
	if len(layout.Missing) == 0 {
		return layout, nil
	}

	holders := map[string]string{}
	hold := func(datasets []Dataset) {
		for _, d := range datasets {
			if _, ok := holders[d.Mountpoint]; !ok {
				holders[d.Mountpoint] = d.Name
			}
		}
	}
	hold(datasets)
	for _, other := range pools {
		if other == pool {
			continue
		}
		others, err := s.system.Datasets(ctx, other)
		if err != nil {
			return Layout{}, err
		}
		hold(others)
	}
	for i, d := range layout.Datasets {
		if !d.Present {
			layout.Datasets[i].MountpointTakenBy = holders[d.ExpectedMountpoint]
		}
	}
	return layout, nil
}

// judgeLayout compares mountpoints, not just names. A replica of the layout
// on a backup pool has every name right and is mounted nowhere.
func judgeLayout(layout Layout, err error) verdict {
	if err != nil {
		return unobserved("the jails datasets", err)
	}
	if misplaced := layout.misplaced(); len(misplaced) > 0 {
		fixes := make([]string, 0, len(misplaced))
		for _, d := range misplaced {
			if d.Name == layout.Pool+"/jails" {
				fixes = append(fixes, "zfs set mountpoint="+d.ExpectedMountpoint+" "+d.Name)
			} else {
				fixes = append(fixes, "zfs inherit mountpoint "+d.Name)
			}
		}
		return verdict{
			status:   CheckFail,
			observed: describeConflicts(misplaced),
			detail: "A dataset of the layout is mounted somewhere else. Jail Deck will not move it, since it may hold live jails; " +
				"the remedy remounts it, so stop what uses it first.",
			remedy: strings.Join(fixes, " && "),
		}
	}
	if taken := layout.taken(); len(taken) > 0 {
		pools := make([]string, 0, len(taken))
		for _, d := range taken {
			if p := poolOf(d.MountpointTakenBy); p != layout.Pool && !slices.Contains(pools, p) {
				pools = append(pools, p)
			}
		}
		remedy := "Move " + taken[0].MountpointTakenBy + " off " + taken[0].ExpectedMountpoint + ", then run jaildeck init."
		if len(pools) > 0 {
			// Most likely the layout is on that other pool.
			remedy = poolRemedy(pools)
		}
		return verdict{
			status:   CheckFail,
			observed: describeConflicts(taken),
			detail:   "Creating the missing datasets would mount them over datasets already there, and Jail Deck will not.",
			remedy:   remedy,
		}
	}
	if len(layout.Missing) > 0 {
		return verdict{
			status:   CheckFail,
			observed: "missing " + strings.Join(layout.Missing, ", "),
			detail:   "Jail Deck creates the missing datasets and touches none that exist; PUT /api/host/layout does the same.",
			remedy:   "jaildeck init",
		}
	}
	return verdict{
		status:   CheckPass,
		observed: layout.Pool + "/jails at " + jailsMountpoint,
		detail:   "media, templates and containers are all present beneath it.",
	}
}

func describeConflicts(conflicts []LayoutDataset) string {
	parts := make([]string, 0, len(conflicts))
	for _, d := range conflicts {
		if d.Present {
			parts = append(parts, fmt.Sprintf("%s is mounted at %s, expected %s", d.Name, d.Mountpoint, d.ExpectedMountpoint))
		} else {
			parts = append(parts, fmt.Sprintf("%s would be mounted at %s, which %s already holds", d.Name, d.ExpectedMountpoint, d.MountpointTakenBy))
		}
	}
	return strings.Join(parts, "; ")
}

func poolOf(dataset string) string {
	pool, _, _ := strings.Cut(dataset, "/")
	return pool
}

// LayoutConflictError refuses an ensure that would have to move a mounted
// dataset, or mount a new one over it.
type LayoutConflictError struct {
	Conflicts []LayoutDataset
}

func (e *LayoutConflictError) Error() string {
	return describeConflicts(e.Conflicts) + "; refusing to move a mounted dataset or mount over one, since either may hold live jails"
}

// Layout reports what exists of the jails layout. It reads and never writes.
func (s *Service) Layout(ctx context.Context) (Layout, error) {
	pool, pools, v, err := s.choosePool(ctx)
	if v.status != CheckPass {
		return Layout{}, poolError(v, err)
	}
	layout, err := s.readLayout(ctx, pool, pools)
	if err != nil {
		return Layout{}, commandAppError("read the jails datasets", err)
	}
	return layout, nil
}

// EnsureLayout brings the layout to its full shape and is safe to repeat. It
// creates only what is missing, parent first; when nothing is, it runs no
// zfs create at all. A dataset mounted elsewhere refuses the whole request
// before anything is created, because fixing it would move live data.
func (s *Service) EnsureLayout(ctx context.Context) (LayoutChange, error) {
	// A client disconnecting must not leave the layout half-made; the runner
	// still bounds each command (JD-014).
	ctx = context.WithoutCancel(ctx)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	layout, err := s.Layout(ctx)
	if err != nil {
		return LayoutChange{}, err
	}
	if conflicts := append(layout.misplaced(), layout.taken()...); len(conflicts) > 0 {
		return LayoutChange{}, &LayoutConflictError{Conflicts: conflicts}
	}

	created := make([]string, 0, len(layout.Missing))
	for _, d := range layout.Datasets {
		if d.Present {
			continue
		}
		// Only the parent is given a mountpoint; the children inherit it.
		var props map[string]string
		if d.Name == layout.Pool+"/jails" {
			props = map[string]string{"mountpoint": jailsMountpoint}
		}
		result, err := s.system.CreateDataset(ctx, d.Name, props)
		s.logCreate(ctx, d.Name, result.Command, err)
		if err != nil {
			what := "create " + d.Name
			if len(created) > 0 {
				what += " after creating " + strings.Join(created, ", ")
			}
			return LayoutChange{}, commandAppError(what, err)
		}
		created = append(created, d.Name)
	}
	if len(created) == 0 {
		return LayoutChange{Layout: layout, Created: created}, nil
	}

	// Report the layout as the system now has it, not as this request
	// believes it made it (JD-011).
	after, err := s.Layout(ctx)
	if err != nil {
		return LayoutChange{}, err
	}
	return LayoutChange{Layout: after, Changed: true, Created: created}, nil
}

// logCreate records one zfs create. One entry per command, so an ensure that
// creates three datasets writes three and ?targets=<dataset> finds each.
func (s *Service) logCreate(ctx context.Context, name, command string, opErr error) {
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: OperationCreateDataset,
		Target:    name,
		Command:   command,
		Success:   opErr == nil,
	}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		var cmdErr *system.CommandError
		if errors.As(opErr, &cmdErr) {
			entry.ExitCode = cmdErr.Result.ExitCode
			entry.Error = cmdErr.Unwrap().Error()
		} else {
			entry.Error = opErr.Error()
			entry.ExitCode = -1
		}
	}
	if err := s.operationLogger.Log(ctx, entry); err != nil {
		common.Logger(ctx, "host").Error("failed to write operation log",
			slog.String("operation", OperationCreateDataset),
			slog.String("target", name),
			slog.Any("err", err))
	}
}

// poolError answers a request that needs a pool when there is none to use. A
// failed choice is the host's state conflicting with the request, not a
// fault, so it is a 409 that says what to set.
func poolError(v verdict, err error) error {
	if err != nil {
		return commandAppError("list the ZFS pools", err)
	}
	msg := v.detail
	if v.observed != "" {
		msg += " Found: " + v.observed + "."
	}
	if v.remedy != "" {
		msg += " " + v.remedy
	}
	return common.NewAppError(http.StatusConflict, msg)
}

func commandAppError(what string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewAppError(http.StatusGatewayTimeout, fmt.Sprintf("%s: timed out and may not have completed", what))
	}
	msg := err.Error()
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		msg = cmdErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError, fmt.Sprintf("failed to %s: %s", what, msg))
}

// unobserved is the verdict for a check that could not ask the host.
func unobserved(what string, err error) verdict {
	return verdict{
		status: CheckUnknown,
		detail: fmt.Sprintf("Could not read %s: %v", what, err),
	}
}
