package networks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// Settings is the default network as configured (JAILDECK_VM_*), and the
// range tenant networks are allocated from (JAILDECK_NETWORK_RANGE, JD-043).
type Settings struct {
	Switch    string
	Address   string
	Egress    string
	AnchorDir string
	Range     string
}

type Service struct {
	system   System
	audit    audit.Logger
	settings Settings
	// ensureMu serialises everything that writes: two ensures at once would
	// both find the switch missing, two creates could take the same /24, and
	// the default network and the tenant networks share one anchor file.
	ensureMu sync.Mutex
}

func NewService(system System, auditLogger audit.Logger, settings Settings) *Service {
	return &Service{system: system, audit: auditLogger, settings: settings}
}

// anchorFile is where Jail Deck writes the anchor's rules, and what the
// operator's pf.conf loads them from at every boot (JD-040).
func (s *Service) anchorFile() string {
	return filepath.Join(s.settings.AnchorDir, "vmnet.conf")
}

// PFLines are the three lines the operator adds to pf.conf once: NAT and
// filter rules evaluate jaildeck/*, and the anchor's rules are loaded from
// Jail Deck's file whenever PF loads pf.conf. The filter line is unused by
// the default network; it is there so tenant networks' rules (#118) need no
// second edit.
func (s *Service) PFLines() []string {
	return []string{
		`nat-anchor "jaildeck/*"`,
		`anchor "jaildeck/*"`,
		fmt.Sprintf(`load anchor %q from %q`, Anchor, s.anchorFile()),
	}
}

func (s *Service) unavailable() error {
	return common.NewAppError(http.StatusNotImplemented,
		"this Jail Deck is running without the host access it needs to manage networks")
}

// prefix is the configured address, checked: IPv4, with a prefix that leaves
// room for guests.
func (s *Service) prefix() (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s.settings.Address)
	if err != nil || !p.Addr().Is4() || p.Bits() > 30 || p.Addr() == p.Masked().Addr() {
		return netip.Prefix{}, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"JAILDECK_VM_ADDRESS is %q; it must be the host's IPv4 address on the network with its prefix, "+
				"30 bits or fewer, such as 10.88.0.1/24", s.settings.Address))
	}
	return p, nil
}

func (s *Service) egress(ctx context.Context) (string, error) {
	if s.settings.Egress != "" {
		return s.settings.Egress, nil
	}
	return s.system.DefaultRouteInterface(ctx)
}

// anchorContent is the anchor file Jail Deck writes: NAT for the default
// network (JD-040) and, for each tenant network, its NAT and the rules that
// keep it apart (JD-043). With no tenant network it is the default network's
// file exactly as it has always been, so a host that set the default network
// up does not see it as changed.
//
// For each tenant network, the pass to its own gateway comes before the block
// of everything else the host has ("self") and of every other VM network:
// measured, the block first took the gateway with it, and with it the host's
// own traffic to the guests. The default network reaches no tenant network.
func anchorContent(subnet netip.Prefix, defaultBridge, egress string, tenants []Network) string {
	var b strings.Builder
	b.WriteString("# Written by Jail Deck: NAT for the default VM network (JD-040).\n" +
		"# Rewritten when it differs; edits here do not last.\n")
	fmt.Fprintf(&b, "nat on %s inet from %s to any -> (%s)\n", egress, subnet, egress)
	all := []string{subnet.String()}
	for _, t := range tenants {
		if t.Subnet != "" {
			all = append(all, t.Subnet)
		}
	}
	if len(all) == 1 {
		return b.String()
	}
	b.WriteString("\n# Tenant networks (JD-043): each reaches its own gateway and the outside,\n" +
		"# and nothing else of the host's or of another network's.\n")
	others := func(own string) string {
		list := make([]string, 0, len(all))
		for _, s := range all {
			if s != own {
				list = append(list, s)
			}
		}
		return strings.Join(append(list, "self"), ", ")
	}
	for _, t := range tenants {
		if t.Subnet == "" {
			continue
		}
		fmt.Fprintf(&b, "\n# %s\n", t.Name)
		fmt.Fprintf(&b, "nat on %s inet from %s to any -> (%s)\n", egress, t.Subnet, egress)
		fmt.Fprintf(&b, "pass in quick on %s inet from %s to %s no state\n", t.Bridge, t.Subnet, t.Gateway)
		fmt.Fprintf(&b, "block in quick on %s inet from %s to { %s } no state\n", t.Bridge, t.Subnet, others(t.Subnet))
	}
	b.WriteString("\n# The default network reaches no tenant network.\n")
	fmt.Fprintf(&b, "block in quick on %s inet from %s to { %s } no state\n",
		defaultBridge, subnet, strings.Join(all[1:], ", "))
	return b.String()
}

// Default reads every part of the default network off the host.
func (s *Service) Default(ctx context.Context) (Default, error) {
	if s.system == nil {
		return Default{}, s.unavailable()
	}
	p, err := s.prefix()
	if err != nil {
		return Default{}, err
	}
	switches, err := s.system.Switches(ctx)
	if err != nil {
		return Default{}, readError("vm-bhyve's switches", err)
	}
	egress, egressErr := s.egress(ctx)

	d := Default{
		Switch:  s.settings.Switch,
		Address: p.String(),
		Subnet:  p.Masked().String(),
		Gateway: p.Addr().String(),
		Egress:  egress,
	}
	add := func(part Part) { d.Parts = append(d.Parts, part) }

	// The switch, its address, its private ports.
	var sw *Switch
	for i := range switches {
		if switches[i].Name == s.settings.Switch {
			sw = &switches[i]
		}
	}
	switch {
	case sw == nil:
		add(Part{ID: PartSwitch, Title: "A vm-bhyve switch for the network", Owner: OwnerJailDeck, Status: PartFail,
			Detail: "There is no switch named " + s.settings.Switch + "."})
	case sw.Type != "standard":
		add(Part{ID: PartSwitch, Title: "A vm-bhyve switch for the network", Owner: OwnerOperator, Status: PartFail,
			Observed: sw.Type, Detail: "A switch named " + sw.Name + " exists and is a " + sw.Type +
				" switch; Jail Deck sets up only a standard one, a bridge of its own.",
			Remedy: "Rename or remove that switch, or set JAILDECK_VM_SWITCH to another name."})
	default:
		add(Part{ID: PartSwitch, Title: "A vm-bhyve switch for the network", Owner: OwnerJailDeck, Status: PartPass,
			Observed: sw.Name + " (" + sw.Bridge + ")"})
	}
	switch {
	case sw == nil || sw.Type != "standard":
		add(Part{ID: PartAddress, Title: "The host's address on it", Owner: OwnerJailDeck, Status: PartUnknown,
			Detail: "Waits for the switch."})
	case sw.Address == "":
		add(Part{ID: PartAddress, Title: "The host's address on it", Owner: OwnerJailDeck, Status: PartFail,
			Detail: "The switch has no address, so guests on it have no gateway."})
	case sw.Address != p.String():
		add(Part{ID: PartAddress, Title: "The host's address on it", Owner: OwnerOperator, Status: PartFail,
			Observed: sw.Address, Detail: "The switch's address is not the one configured, " + p.String() +
				"; Jail Deck does not move an address guests may already be using.",
			Remedy: "Set JAILDECK_VM_ADDRESS=" + sw.Address + " to adopt it, or change the switch's address yourself."})
	default:
		add(Part{ID: PartAddress, Title: "The host's address on it", Owner: OwnerJailDeck, Status: PartPass, Observed: sw.Address})
	}
	switch {
	case sw == nil || sw.Type != "standard":
		add(Part{ID: PartPrivate, Title: "Guests kept apart from each other", Owner: OwnerJailDeck, Status: PartUnknown,
			Detail: "Waits for the switch."})
	case !sw.Private:
		add(Part{ID: PartPrivate, Title: "Guests kept apart from each other", Owner: OwnerJailDeck, Status: PartFail,
			Detail: "The switch's ports are not private, so its guests can reach each other directly."})
	default:
		add(Part{ID: PartPrivate, Title: "Guests kept apart from each other", Owner: OwnerJailDeck, Status: PartPass,
			Observed: "private", Detail: "Guests reach the host and the outside, not each other (measured)."})
	}

	// Forwarding, now and at boot.
	if v, err := s.system.Sysctl(ctx, "net.inet.ip.forwarding"); err != nil {
		add(Part{ID: PartForwarding, Title: "IP forwarding", Owner: OwnerJailDeck, Status: PartUnknown, Detail: err.Error()})
	} else if strings.TrimSpace(v) != "1" {
		add(Part{ID: PartForwarding, Title: "IP forwarding", Owner: OwnerJailDeck, Status: PartFail, Observed: v,
			Detail: "The host does not forward packets, so nothing from a guest gets past it."})
	} else {
		add(Part{ID: PartForwarding, Title: "IP forwarding", Owner: OwnerJailDeck, Status: PartPass, Observed: v})
	}
	if v, err := s.system.RCConf(ctx, "gateway_enable"); err != nil {
		add(Part{ID: PartForwardingAtBoot, Title: "IP forwarding at boot", Owner: OwnerJailDeck, Status: PartUnknown, Detail: err.Error()})
	} else if !strings.EqualFold(strings.TrimSpace(v), "YES") {
		add(Part{ID: PartForwardingAtBoot, Title: "IP forwarding at boot", Owner: OwnerJailDeck, Status: PartFail, Observed: v,
			Detail: "rc.conf's gateway_enable is not YES, so forwarding is off again after a reboot."})
	} else {
		add(Part{ID: PartForwardingAtBoot, Title: "IP forwarding at boot", Owner: OwnerJailDeck, Status: PartPass, Observed: v})
	}

	// NAT: the anchor file, PF itself, the operator's pf.conf, the loaded anchor.
	// The file is the default network's and the tenant networks' (JD-043).
	want := ""
	tenants, tenantsErr := s.tenantNetworks(ctx)
	switch {
	case tenantsErr != nil:
		add(Part{ID: PartAnchorFile, Title: "Jail Deck's NAT rules, on disk", Owner: OwnerJailDeck, Status: PartUnknown,
			Detail: tenantsErr.Error()})
	case egressErr != nil:
		add(Part{ID: PartAnchorFile, Title: "Jail Deck's NAT rules, on disk", Owner: OwnerJailDeck, Status: PartUnknown,
			Detail: "Could not read the default route: " + egressErr.Error()})
	case egress == "":
		add(Part{ID: PartAnchorFile, Title: "Jail Deck's NAT rules, on disk", Owner: OwnerOperator, Status: PartFail,
			Detail: "The host has no default route, so there is no interface to send the guests out of.",
			Remedy: "Set JAILDECK_VM_EGRESS to the interface NAT should use."})
	default:
		want = anchorContent(p.Masked(), "vm-"+s.settings.Switch, egress, tenants)
		have, err := s.system.AnchorFile(ctx, s.anchorFile())
		switch {
		case err != nil:
			add(Part{ID: PartAnchorFile, Title: "Jail Deck's NAT rules, on disk", Owner: OwnerJailDeck, Status: PartUnknown, Detail: err.Error()})
		case have != want:
			add(Part{ID: PartAnchorFile, Title: "Jail Deck's NAT rules, on disk", Owner: OwnerJailDeck, Status: PartFail,
				Observed: s.anchorFile(), Detail: "The file is missing or says something else."})
		default:
			add(Part{ID: PartAnchorFile, Title: "Jail Deck's NAT rules, on disk", Owner: OwnerJailDeck, Status: PartPass, Observed: s.anchorFile()})
		}
	}

	pf, pfErr := s.system.PF(ctx)
	switch {
	case pfErr != nil:
		add(Part{ID: PartPF, Title: "PF enabled", Owner: OwnerOperator, Status: PartUnknown, Detail: pfErr.Error()})
	case !pf.Enabled:
		observed := "not loaded"
		if pf.Loaded {
			observed = "loaded, disabled"
		}
		add(Part{ID: PartPF, Title: "PF enabled", Owner: OwnerOperator, Status: PartFail, Observed: observed,
			Detail: "NAT is PF's. Without it guests reach the host and nothing beyond it. " +
				"Enabling PF over SSH can drop that session when the ruleset keeps state, which most do; " +
				"a new connection works at once (measured).",
			Remedy: "sysrc pf_enable=YES && service pf start"})
	default:
		add(Part{ID: PartPF, Title: "PF enabled", Owner: OwnerOperator, Status: PartPass, Observed: "enabled"})
	}

	missing := make([]string, 0)
	for _, line := range s.PFLines() {
		if !hasLine(pf.RulesText, line) {
			missing = append(missing, line)
		}
	}
	rulesFile := pf.RulesFile
	if rulesFile == "" {
		rulesFile = "/etc/pf.conf"
	}
	switch {
	case pfErr != nil:
		add(Part{ID: PartPFRules, Title: "pf.conf includes Jail Deck's anchors", Owner: OwnerOperator, Status: PartUnknown,
			Detail: "Waits for PF to be readable."})
	case len(missing) > 0:
		add(Part{ID: PartPFRules, Title: "pf.conf includes Jail Deck's anchors", Owner: OwnerOperator, Status: PartFail,
			Observed: rulesFile, Detail: "pf.conf lacks " + fmt.Sprint(len(missing)) + " of the three lines that let " +
				"Jail Deck's rules in and load them at every boot. nat-anchor goes with the other nat rules, " +
				"anchor with the filter rules; then reload PF.",
			Remedy: strings.Join(s.PFLines(), "\n") + "\n# then: service pf reload"})
	case pf.Enabled && !pf.NatAnchor:
		add(Part{ID: PartPFRules, Title: "pf.conf includes Jail Deck's anchors", Owner: OwnerOperator, Status: PartFail,
			Observed: rulesFile, Detail: "The file has the lines and the loaded ruleset does not yet.",
			Remedy: "service pf reload"})
	default:
		add(Part{ID: PartPFRules, Title: "pf.conf includes Jail Deck's anchors", Owner: OwnerOperator, Status: PartPass, Observed: rulesFile})
	}

	if !pf.Enabled || want == "" {
		add(Part{ID: PartAnchorLoaded, Title: "NAT rules loaded", Owner: OwnerJailDeck, Status: PartUnknown,
			Detail: "Waits for PF and the rules on disk."})
	} else if rules, err := s.system.AnchorRules(ctx, Anchor); err != nil {
		add(Part{ID: PartAnchorLoaded, Title: "NAT rules loaded", Owner: OwnerJailDeck, Status: PartUnknown, Detail: err.Error()})
	} else if !natLoaded(rules, p.Masked(), egress) {
		add(Part{ID: PartAnchorLoaded, Title: "NAT rules loaded", Owner: OwnerJailDeck, Status: PartFail,
			Detail: "Anchor " + Anchor + " does not translate " + p.Masked().String() + " out of " + egress + "."})
	} else {
		add(Part{ID: PartAnchorLoaded, Title: "NAT rules loaded", Owner: OwnerJailDeck, Status: PartPass,
			Observed: strings.Join(rules, "; ")})
	}

	d.Usable = true
	d.Ready = true
	for _, part := range d.Parts {
		if part.Status != PartPass {
			d.Ready = false
			if part.ID == PartSwitch || part.ID == PartAddress || part.ID == PartPrivate {
				d.Usable = false
			}
		}
	}
	return d, nil
}

// hasLine is whether text holds want as a line of its own, possibly followed
// by options. Not a substring search: `anchor "jaildeck/*"` is inside
// `nat-anchor "jaildeck/*"`, and a pf.conf with only the latter must not pass
// for having both.
func hasLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == want || strings.HasPrefix(line, want+" ") || strings.HasPrefix(line, want+"\t") {
			return true
		}
	}
	return false
}

// natLoaded is whether pfctl's listing of the anchor translates the subnet
// out of egress. pfctl prints the rule back in its own form ("... -> (em0)
// round-robin", measured), so the parts are matched, not the line.
func natLoaded(rules []string, subnet netip.Prefix, egress string) bool {
	for _, r := range rules {
		if strings.HasPrefix(r, "nat on "+egress+" ") && strings.Contains(r, "from "+subnet.String()+" ") &&
			strings.Contains(r, "-> ("+egress+")") {
			return true
		}
	}
	return false
}

// EnsureDefault puts in place every part of the default network that is Jail
// Deck's and not there yet, in order, and is safe to repeat (JD-040). It
// refuses rather than clobbers: a switch of that name that is not a standard
// one, or whose address is not the configured one, stops it before anything
// runs. The operator's parts -- PF and pf.conf -- are reported, not touched.
func (s *Service) EnsureDefault(ctx context.Context) (Ensured, error) {
	ctx = context.WithoutCancel(ctx)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()

	before, err := s.Default(ctx)
	if err != nil {
		return Ensured{}, err
	}
	failing := map[string]Part{}
	for _, part := range before.Parts {
		if part.Status != PartFail {
			continue
		}
		if part.Owner == OwnerOperator && (part.ID == PartSwitch || part.ID == PartAddress) {
			return Ensured{}, common.NewAppError(http.StatusConflict, part.Detail+" "+part.Remedy)
		}
		// Only Jail Deck's own parts are put in place. An operator's part
		// that fails -- PF, pf.conf, no default route -- is reported, and
		// nothing that depends on it is done half-right instead.
		if part.Owner == OwnerJailDeck {
			failing[part.ID] = part
		}
	}

	done := make([]string, 0)
	step := func(id, operation, target string, run func() (string, error)) error {
		if _, ok := failing[id]; !ok {
			return nil
		}
		command, err := run()
		s.log(ctx, operation, target, command, err)
		if err != nil {
			what := "put the default network's " + id + " in place"
			if len(done) > 0 {
				what += " after " + strings.Join(done, ", ")
			}
			return operationError(what, err)
		}
		done = append(done, id)
		return nil
	}
	sw := s.settings.Switch
	steps := []struct {
		id, operation, target string
		run                   func() (string, error)
	}{
		{PartSwitch, OperationCreateSwitch, sw, func() (string, error) { return s.system.CreateSwitch(ctx, sw) }},
		{PartAddress, OperationSetSwitchAddress, sw, func() (string, error) { return s.system.SetSwitchAddress(ctx, sw, before.Address) }},
		{PartPrivate, OperationSetSwitchPrivate, sw, func() (string, error) { return s.system.SetSwitchPrivate(ctx, sw) }},
		{PartForwarding, OperationEnableForwarding, "net.inet.ip.forwarding", func() (string, error) {
			return s.system.SetSysctl(ctx, "net.inet.ip.forwarding", "1")
		}},
		{PartForwardingAtBoot, OperationEnableForwardingAtBoot, "gateway_enable", func() (string, error) {
			return s.system.SetRCConf(ctx, "gateway_enable", "YES")
		}},
		{PartAnchorFile, OperationWriteAnchor, s.anchorFile(), func() (string, error) {
			p, _ := s.prefix()
			tenants, err := s.tenantNetworks(ctx)
			if err != nil {
				return "", err
			}
			return s.system.WriteAnchorFile(ctx, s.anchorFile(),
				anchorContent(p.Masked(), "vm-"+s.settings.Switch, before.Egress, tenants))
		}},
	}
	// A switch just created has no address and shared ports: both follow.
	if _, ok := failing[PartSwitch]; ok {
		failing[PartAddress] = Part{}
		failing[PartPrivate] = Part{}
	}
	for _, st := range steps {
		if err := step(st.id, st.operation, st.target, st.run); err != nil {
			return Ensured{}, err
		}
	}
	// The anchor is loaded whenever PF is up and the rules are not, which
	// includes the file having just been rewritten.
	if pfUp(before) && (failing[PartAnchorFile].ID != "" || !passes(before, PartAnchorLoaded)) {
		failing[PartAnchorLoaded] = Part{ID: PartAnchorLoaded}
		if err := step(PartAnchorLoaded, OperationLoadAnchor, Anchor, func() (string, error) {
			return s.system.LoadAnchor(ctx, Anchor, s.anchorFile())
		}); err != nil {
			return Ensured{}, err
		}
	}

	after, err := s.Default(ctx)
	if err != nil {
		return Ensured{}, err
	}
	ensured := Ensured{Default: after, Changed: len(done) > 0, Done: done}
	for _, id := range done {
		if id == PartPrivate {
			ensured.Note = "Private ports apply to guests as they next start: one already running on " +
				sw + " still reaches the others until it is restarted."
		}
	}
	return ensured, nil
}

func pfUp(d Default) bool { return passes(d, PartPF) }

func passes(d Default, id string) bool {
	for _, part := range d.Parts {
		if part.ID == id {
			return part.Status == PartPass
		}
	}
	return false
}

func (s *Service) log(ctx context.Context, operation, target, command string, opErr error) {
	if s.audit == nil {
		return
	}
	entry := audit.Entry{Timestamp: time.Now(), Operation: operation, Target: target, Command: command, Success: opErr == nil}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		entry.ExitCode = -1
		entry.Error = opErr.Error()
		var commandErr *system.CommandError
		if errors.As(opErr, &commandErr) {
			entry.ExitCode = commandErr.Result.ExitCode
			entry.Error = commandErr.Unwrap().Error()
		}
	}
	if err := s.audit.Log(ctx, entry); err != nil {
		common.Logger(ctx, "networks").Error("failed to write operation log",
			slog.String("operation", operation), slog.String("target", target), slog.Any("err", err))
	}
}

func operationError(what string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	message := err.Error()
	var commandErr *system.CommandError
	if errors.As(err, &commandErr) {
		message = commandErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError, "could not "+what+": "+message)
}

func readError(what string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return common.NewAppError(http.StatusInternalServerError, "could not read "+what+": "+err.Error())
}
