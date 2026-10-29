package freebsd

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

// From isengard: a jdn bridge made as CreateTenantBridge and SetBridgeAddress
// make one, with an epair on it (net_tenant_bridge, net_bridge_group), and
// the measurements of capture-tenant-network-induced.sh (JD-043).

func TestTenantBridgesReadsOnlyTheJdnBridges(t *testing.T) {
	var asked []string
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		asked = append(asked, cmd.String())
		switch cmd.String() {
		case "ifconfig -g bridge":
			return replay(t, "isengard-induced", "net_bridge_group")
		case "ifconfig -f inet:cidr jdn249":
			return replay(t, "isengard-induced", "net_tenant_bridge")
		}
		t.Errorf("unexpected %s", cmd)
		return system.CommandResult{}, errors.New("unexpected")
	}))
	bridges, err := a.TenantBridges(context.Background())
	if err != nil || len(bridges) != 1 {
		t.Fatalf("bridges = %+v, %v", bridges, err)
	}
	b := bridges[0]
	if b.Name != "jdn249" || b.Description != "jaildeck:network=acme-prod;tenant=acme" ||
		b.Address != netip.MustParsePrefix("10.92.249.1/24") || !slices.Equal(b.Members, []string{"epair0a"}) {
		t.Errorf("bridge = %+v", b)
	}
	if slices.ContainsFunc(asked, func(c string) bool { return strings.Contains(c, "vm-") }) {
		t.Errorf("read vm-bhyve's own bridges: %v", asked)
	}
}

// Constructed: `ifconfig -a -f inet:cidr` in the form net_tenant_bridge shows,
// over a loopback, an uplink and a bridge; documentation addresses only.
func TestAddressesInUse(t *testing.T) {
	a := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "em0: flags=1008843<UP> metric 0 mtu 1500\n" +
			"\tinet 192.0.2.10/24 broadcast 192.0.2.255\n" +
			"lo0: flags=1008049<UP,LOOPBACK> metric 0 mtu 16384\n" +
			"\tinet6 ::1 prefixlen 128\n\tinet 127.0.0.1/8\n" +
			"jdn0: flags=1008843<UP> metric 0 mtu 1500\n\tinet 10.92.0.1/24 broadcast 10.92.0.255\n"}, nil
	}))
	got, err := a.AddressesInUse(context.Background())
	want := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.92.0.0/24")}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("addresses = %v, %v; want %v", got, err, want)
	}
}

func TestTenantNetworkWrites(t *testing.T) {
	dir := t.TempDir()
	var ran []string
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		ran = append(ran, cmd.String())
		if cmd.String() == "vm datastore list" {
			return system.CommandResult{Stdout: "NAME TYPE PATH ZFS\ndefault zfs " + dir + " tank/vm\n"}, nil
		}
		return system.CommandResult{}, nil
	}))
	ctx := context.Background()
	_, _ = a.CreateTenantBridge(ctx, "jdn0", "jaildeck:network=acme;tenant=acme")
	_, _ = a.SetBridgeAddress(ctx, "jdn0", "10.92.0.1/24")
	_, _ = a.CreateManualSwitch(ctx, "acme", "jdn0")
	command, _ := a.DestroySwitch(ctx, "acme")
	_, _ = a.DestroyBridge(ctx, "jdn0")
	conf := filepath.Join(dir, ".config", "system.conf")
	want := []string{
		// The address cannot ride along: "inet: bad value" after the other
		// options, measured.
		"ifconfig bridge create name jdn0 description jaildeck:network=acme;tenant=acme up",
		"ifconfig jdn0 inet 10.92.0.1/24",
		"vm switch create -t manual -b jdn0 acme",
		"vm datastore list",
		"vm switch destroy acme",
		// What vm switch destroy leaves of a manual switch, measured.
		"sysrc -i -f " + conf + " -x bridge_acme",
		"ifconfig jdn0 destroy",
	}
	if !slices.Equal(ran, want) {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(ran, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(command, "-x bridge_acme") {
		t.Errorf("destroy command = %q", command)
	}
}

// Guests are found by their configuration, running or not; jails by their
// stanza, in jail.conf.d and in jail.conf.
func TestWhoUsesANetwork(t *testing.T) {
	store := t.TempDir()
	for guest, conf := range map[string]string{
		"web":   "loader=\"uefi\"\nnetwork0_type=\"virtio-net\"\nnetwork0_switch=\"acme\"\n",
		"db":    "network0_switch=\"jaildeck\"\nnetwork1_switch=\"acme\"\n",
		"other": "network0_switch=\"acme-prod\"\n",
	} {
		_ = os.MkdirAll(filepath.Join(store, guest), 0o755)
		_ = os.WriteFile(filepath.Join(store, guest, guest+".conf"), []byte(conf), 0o644)
	}
	jails := t.TempDir()
	confDir := filepath.Join(jails, "jail.conf.d")
	_ = os.MkdirAll(confDir, 0o755)
	_ = os.WriteFile(filepath.Join(confDir, "api.conf"), []byte("api {\n  vnet;\n  exec.prestart += \"ifconfig jdn0 addm ${epair}a\";\n}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(confDir, "near.conf"), []byte("near {\n  exec.prestart += \"ifconfig jdn01 addm e\";\n}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(jails, "jail.conf"), []byte("legacy {\n  vnet.interface = \"jdn0b\";\n}\nold { bridge = jdn0; }\n"), 0o644)

	a := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "NAME TYPE PATH ZFS\ndefault zfs " + store + " tank/vm\n"}, nil
	}))
	a.confDir = confDir
	guests, err := a.SwitchUsers(context.Background(), "acme")
	if err != nil || !slices.Equal(guests, []string{"db", "web"}) {
		t.Errorf("guests = %v, %v; want db and web, not acme-prod's", guests, err)
	}
	users, err := a.BridgeUsers(context.Background(), "jdn0")
	if err != nil || !slices.Equal(users, []string{"api", filepath.Join(jails, "jail.conf")}) {
		t.Errorf("jails = %v, %v; want api and jail.conf, not jdn01's or jdn0b's", users, err)
	}
}
