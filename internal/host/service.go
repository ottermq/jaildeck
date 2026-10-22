package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	system HostSystem
}

func NewService(system HostSystem) *Service {
	return &Service{system: system}
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

// unobserved is the verdict for a check that could not ask the host.
func unobserved(what string, err error) verdict {
	return verdict{
		status: CheckUnknown,
		detail: fmt.Sprintf("Could not read %s: %v", what, err),
	}
}
