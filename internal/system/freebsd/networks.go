package freebsd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/system"
)

// pfTimeout bounds a pfctl call. Loading a one-rule anchor is instant; this
// is a backstop.
const pfTimeout = 30 * time.Second

// Switches reads `vm switch list`:
//
//	NAME      TYPE      IFACE        ADDRESS       PRIVATE  MTU  VLAN  PORTS
//	jd-netsw  standard  vm-jd-netsw  10.81.0.1/24  yes      -    -     -
//
// No column holds a space, and "-" is nothing. vm-bhyve not configured is
// answered in its own words, as a 409: there is no network to speak of yet.
func (a *Adapter) Switches(ctx context.Context) ([]networks.Switch, error) {
	cmd := system.Command{Name: "vm", Args: []string{"switch", "list"}, Timeout: vmTimeout}
	result, err := a.runner.Run(ctx, cmd)
	if errors.Is(err, system.ErrCommandNotFound) {
		return nil, common.NewAppError(http.StatusConflict, "vm-bhyve is not installed")
	}
	if err != nil {
		if exited(result, err) {
			if problem := vmProblem(result.Stdout, result.Stderr); problem != "" {
				return nil, common.NewAppError(http.StatusConflict, "vm-bhyve is not configured: "+problem)
			}
		}
		return nil, vmCommandError(cmd, result, err)
	}
	return parseSwitchList(result.Stdout), nil
}

func parseSwitchList(out string) []networks.Switch {
	switches := make([]networks.Switch, 0)
	for i, line := range nonEmptyLines(out) {
		fields := strings.Fields(line)
		if i == 0 && len(fields) > 0 && fields[0] == "NAME" {
			continue
		}
		if len(fields) < 5 {
			continue
		}
		switches = append(switches, networks.Switch{
			Name: fields[0], Type: fields[1], Bridge: dashless(fields[2]),
			Address: dashless(fields[3]), Private: fields[4] == "yes",
		})
	}
	return switches
}

func (a *Adapter) RCConf(ctx context.Context, name string) (string, error) {
	result, err := a.runHost(ctx, system.Command{Name: "sysrc", Args: []string{"-n", name}})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

// DefaultRouteInterface reads `route -n get default`'s "interface:" line. A
// host with no default route is answered "", not an error.
func (a *Adapter) DefaultRouteInterface(ctx context.Context) (string, error) {
	result, err := a.runHost(ctx, system.Command{Name: "route", Args: []string{"-n", "get", "default"}})
	if err != nil {
		if exited(result, err) && (strings.Contains(result.Stderr, "not been found") || strings.Contains(result.Stderr, "not in table")) {
			return "", nil
		}
		return "", err
	}
	for _, line := range nonEmptyLines(result.Stdout) {
		if v, ok := strings.CutPrefix(line, "interface:"); ok {
			return strings.TrimSpace(v), nil
		}
	}
	return "", nil
}

// PF reads what the default network needs of PF: whether it is loaded and
// enabled, whether the loaded main ruleset evaluates jaildeck/*'s NAT (pfctl
// -s nat prints `nat-anchor "jaildeck/*" all`, measured), and the text of the file
// rc.d/pf loads, which is what says whether the anchor survives a reboot.
func (a *Adapter) PF(ctx context.Context) (networks.PF, error) {
	status, err := a.PFStatus(ctx)
	if err != nil {
		return networks.PF{}, err
	}
	pf := networks.PF{Loaded: status.Loaded, Enabled: status.Enabled}
	if status.Loaded {
		nat, err := a.runHost(ctx, system.Command{Name: "pfctl", Args: []string{"-s", "nat"}, Timeout: pfTimeout})
		if err != nil {
			return networks.PF{}, err
		}
		pf.NatAnchor = hasRuleLine(nat.Stdout, `nat-anchor "jaildeck/*"`)
	}
	file, err := a.RCConf(ctx, "pf_rules")
	if err != nil {
		return networks.PF{}, err
	}
	pf.RulesFile = file
	if b, err := os.ReadFile(file); err == nil {
		pf.RulesText = string(b)
	}
	return pf, nil
}

// hasRuleLine is whether pfctl's listing has a rule beginning with want:
// pfctl prints `nat-anchor "jaildeck/*" all`, and `anchor` is inside
// `nat-anchor`, so a substring search would mistake one for the other.
func hasRuleLine(listing, want string) bool {
	for _, line := range nonEmptyLines(listing) {
		if line == want || strings.HasPrefix(line, want+" ") {
			return true
		}
	}
	return false
}

// AnchorRules lists the NAT rules loaded in an anchor. PF not loaded is no
// rules.
func (a *Adapter) AnchorRules(ctx context.Context, anchor string) ([]string, error) {
	result, err := a.runHost(ctx, system.Command{Name: "pfctl", Args: []string{"-a", anchor, "-s", "nat"}, Timeout: pfTimeout})
	if err != nil {
		if exited(result, err) && strings.Contains(result.Stderr, "/dev/pf") {
			return nil, nil
		}
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

func (a *Adapter) AnchorFile(_ context.Context, path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func (a *Adapter) CreateSwitch(ctx context.Context, name string) (string, error) {
	return a.vmLifecycle(ctx, system.Command{Name: "vm", Args: []string{"switch", "create", name}, Timeout: vmTimeout})
}

// SetSwitchAddress gives the host an address on the switch's bridge. vm-bhyve
// keeps it in its configuration and puts it back when it creates the switch
// at boot.
func (a *Adapter) SetSwitchAddress(ctx context.Context, name, address string) (string, error) {
	return a.vmLifecycle(ctx, system.Command{Name: "vm", Args: []string{"switch", "address", name, address}, Timeout: vmTimeout})
}

// SetSwitchPrivate marks the switch private. vm-bhyve applies it to each
// guest's tap as the guest starts (`ifconfig <bridge> private <tap>`, read in
// its source), so a running guest keeps its shared port until it restarts.
func (a *Adapter) SetSwitchPrivate(ctx context.Context, name string) (string, error) {
	return a.vmLifecycle(ctx, system.Command{Name: "vm", Args: []string{"switch", "private", name, "on"}, Timeout: vmTimeout})
}

func (a *Adapter) SetSysctl(ctx context.Context, name, value string) (string, error) {
	cmd := system.Command{Name: "sysctl", Args: []string{name + "=" + value}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

func (a *Adapter) SetRCConf(ctx context.Context, name, value string) (string, error) {
	cmd := system.Command{Name: "sysrc", Args: []string{name + "=" + value}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

func (a *Adapter) WriteAnchorFile(_ context.Context, path, content string) (string, error) {
	command := "write " + path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return command, err
	}
	return command, writeIfChanged(path, []byte(content))
}

// LoadAnchor validates the file against the anchor with `pfctl -n` and only
// then loads it, replacing the anchor's rules. Measured: a file pfctl refuses
// is reported with its line and reason, and the rules loaded before stay.
func (a *Adapter) LoadAnchor(ctx context.Context, anchor, path string) (string, error) {
	check := system.Command{Name: "pfctl", Args: []string{"-n", "-a", anchor, "-f", path}, Timeout: pfTimeout}
	load := system.Command{Name: "pfctl", Args: []string{"-a", anchor, "-f", path}, Timeout: pfTimeout}
	command := check.String() + " && " + load.String()
	if _, err := a.runHost(ctx, check); err != nil {
		return command, fmt.Errorf("pfctl refused the rules, which were not loaded: %w", err)
	}
	_, err := a.runHost(ctx, load)
	return command, err
}

var _ networks.System = (*Adapter)(nil)
