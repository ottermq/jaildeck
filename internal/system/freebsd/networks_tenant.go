package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/system"
)

// Tenant networks (JD-043), on the host: bridges named jdn<i>, a vm-bhyve
// manual switch on each. Measured on isengard with capture-tenant-network-
// induced.sh, and the bridge's one-call create with this file's commands.

// tenantBridgePrefix is the networks package's bridgePrefix, as an
// interface-name prefix to list by.
const tenantBridgePrefix = "jdn"

// TenantBridges lists the jdn<i> bridges, each from `ifconfig -f inet:cidr`,
// which prints the address with its prefix length.
func (a *Adapter) TenantBridges(ctx context.Context) ([]networks.TenantBridge, error) {
	result, err := a.runHost(ctx, system.Command{Name: "ifconfig", Args: []string{"-g", "bridge"}})
	if err != nil {
		return nil, err
	}
	bridges := make([]networks.TenantBridge, 0)
	for _, name := range strings.Fields(result.Stdout) {
		if !strings.HasPrefix(name, tenantBridgePrefix) {
			continue
		}
		out, err := a.runHost(ctx, system.Command{Name: "ifconfig", Args: []string{"-f", "inet:cidr", name}})
		if err != nil {
			// Destroyed between the two reads.
			continue
		}
		bridges = append(bridges, parseBridge(name, out.Stdout))
	}
	return bridges, nil
}

// parseBridge reads one bridge's `ifconfig -f inet:cidr <name>`:
//
//	jdn3: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST,LOWER_UP> metric 0 mtu 1500
//		description: jaildeck:network=acme-prod;tenant=acme
//		inet 10.92.3.1/24 broadcast 10.92.3.255
//		member: tap1 flags=143<LEARNING,DISCOVER,AUTOEDGE,AUTOPTP>
func parseBridge(name, out string) networks.TenantBridge {
	b := networks.TenantBridge{Name: name, Members: make([]string, 0)}
	for _, line := range nonEmptyLines(out) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "description: "):
			b.Description = strings.TrimPrefix(line, "description: ")
		case strings.HasPrefix(line, "inet ") && !b.Address.IsValid():
			if f := strings.Fields(line); len(f) >= 2 {
				b.Address, _ = netip.ParsePrefix(f[1])
			}
		case strings.HasPrefix(line, "member: "):
			if f := strings.Fields(line); len(f) >= 2 {
				b.Members = append(b.Members, f[1])
			}
		}
	}
	return b
}

// AddressesInUse is every IPv4 prefix on every interface: what a new
// network's /24 must not overlap.
func (a *Adapter) AddressesInUse(ctx context.Context) ([]netip.Prefix, error) {
	result, err := a.runHost(ctx, system.Command{Name: "ifconfig", Args: []string{"-a", "-f", "inet:cidr"}})
	if err != nil {
		return nil, err
	}
	prefixes := make([]netip.Prefix, 0)
	for _, line := range nonEmptyLines(result.Stdout) {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "inet" {
			if p, err := netip.ParsePrefix(f[1]); err == nil {
				prefixes = append(prefixes, p.Masked())
			}
		}
	}
	return prefixes, nil
}

// CreateTenantBridge creates, names, describes and brings up the bridge in
// one call. The address cannot go in the same call -- ifconfig answered
// "inet: bad value" after the other options, measured -- and the call is not
// atomic: it failed with the bridge already made, named and described.
func (a *Adapter) CreateTenantBridge(ctx context.Context, bridge, description string) (string, error) {
	cmd := system.Command{Name: "ifconfig", Args: []string{"bridge", "create", "name", bridge, "description", description, "up"}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

func (a *Adapter) SetBridgeAddress(ctx context.Context, bridge, address string) (string, error) {
	cmd := system.Command{Name: "ifconfig", Args: []string{bridge, "inet", address}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

func (a *Adapter) DestroyBridge(ctx context.Context, bridge string) (string, error) {
	cmd := system.Command{Name: "ifconfig", Args: []string{bridge, "destroy"}}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

// CreateManualSwitch is `vm switch create -t manual -b <bridge> <name>`: a
// switch vm-bhyve puts guests' taps on, on a bridge it neither creates nor
// destroys.
func (a *Adapter) CreateManualSwitch(ctx context.Context, name, bridge string) (string, error) {
	return a.vmLifecycle(ctx, system.Command{Name: "vm",
		Args: []string{"switch", "create", "-t", "manual", "-b", bridge, name}, Timeout: vmTimeout})
}

// DestroySwitch is `vm switch destroy <name>`, and then the bridge_<name>
// line it leaves behind in system.conf for a manual switch -- measured, it
// takes the switch out of switch_list and keeps that line.
func (a *Adapter) DestroySwitch(ctx context.Context, name string) (string, error) {
	dir, _, err := a.vmDatastore(ctx)
	if err != nil {
		return "", err
	}
	destroy := system.Command{Name: "vm", Args: []string{"switch", "destroy", name}, Timeout: vmTimeout}
	tidy := system.Command{Name: "sysrc", Args: []string{"-i", "-f", filepath.Join(dir, ".config", "system.conf"),
		"-x", "bridge_" + name}}
	command := destroy.String() + " && " + tidy.String()
	if _, err := a.vmLifecycle(ctx, destroy); err != nil {
		return command, err
	}
	_, err = a.runHost(ctx, tidy)
	return command, err
}

// networkSwitchKey is a NIC's switch in a guest's configuration.
var networkSwitchKey = regexp.MustCompile(`^network[0-9]+_switch$`)

// SwitchUsers are the guests in the default datastore whose configuration
// puts a NIC on the switch, whether or not they are running.
func (a *Adapter) SwitchUsers(ctx context.Context, name string) ([]string, error) {
	dir, _, err := a.vmDatastore(ctx)
	if err != nil {
		return nil, err
	}
	confs, err := filepath.Glob(filepath.Join(dir, "*", "*.conf"))
	if err != nil {
		return nil, err
	}
	users := make([]string, 0)
	for _, conf := range confs {
		guest := filepath.Base(filepath.Dir(conf))
		if filepath.Base(conf) != guest+".conf" {
			continue
		}
		content, err := os.ReadFile(conf)
		if err != nil {
			return nil, err
		}
		for key, value := range parseVMConf(string(content)) {
			if networkSwitchKey.MatchString(key) && value == name {
				users = append(users, guest)
				break
			}
		}
	}
	slices.Sort(users)
	return users, nil
}

// BridgeUsers are the jails whose configuration names the bridge: a VNET
// jail's stanza adds its epair to it (JD-017). The jail.conf.d files are
// named after their jails; a mention in /etc/jail.conf is reported as that
// file.
func (a *Adapter) BridgeUsers(_ context.Context, bridge string) ([]string, error) {
	word := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(bridge) + `([^A-Za-z0-9_.-]|$)`)
	files, err := filepath.Glob(filepath.Join(a.confDir, "*.conf"))
	if err != nil {
		return nil, err
	}
	// /etc/jail.conf is the directory's name without ".d", as the defaults
	// are: a test's directory brings its own file with it.
	main := strings.TrimSuffix(a.confDir, ".d")
	files = append(files, main)
	users := make([]string, 0)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		if word.Match(content) {
			name := strings.TrimSuffix(filepath.Base(f), ".conf")
			if f == main {
				name = main
			}
			users = append(users, name)
		}
	}
	return users, nil
}
