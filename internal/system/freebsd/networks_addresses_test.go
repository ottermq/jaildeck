package freebsd

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/system"
)

// From capture-network-addresses-induced.sh (nad_*), on isengard: a bridge
// jdcap0 on 10.79.0.0/24 whose jail gave itself .5, then .77 by hand.

// addressHost is a jail.conf.d and a datastore at temporary directories, the
// datastore holding the guest capture-vm-create-induced.sh made (its seed
// gives it 10.77.0.10/24), and a runner that answers arp from a capture.
func addressHost(t *testing.T, arp string, confs map[string]string) *Adapter {
	t.Helper()
	root := t.TempDir()
	confDir := filepath.Join(root, "jail.conf.d")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range confs {
		path := filepath.Join(confDir, name)
		if name == "jail.conf" {
			path = filepath.Join(root, "jail.conf")
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(root, "vm")
	if err := os.MkdirAll(filepath.Join(store, "jd-cap-made", ".cloud-init"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "jd-cap-made", ".cloud-init", "network-config"),
		[]byte(seedFiles(t)["network-config"]), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		switch cmd.String() {
		case "vm datastore list":
			return system.CommandResult{Stdout: "NAME            TYPE        PATH                      ZFS DATASET\n" +
				"default         zfs         " + store + "                 tank/vm\n"}, nil
		case "arp -an -i jdcap0":
			return system.CommandResult{Stdout: captured(t, arp)}, nil
		case "arp -an -i jdnope0":
			return system.CommandResult{Stderr: "arp: interface jdnope0 does not exist\n", ExitCode: 1}, errors.New("exit status 1")
		}
		t.Fatalf("unexpected command %q", cmd.String())
		return system.CommandResult{}, nil
	}))
	a.confDir = confDir
	return a
}

// A stanza as Jail Deck writes one, and one written by hand.
const (
	madeStanza = `web {
	vnet;
	vnet.interface = "epair4b";
	exec.prestart += "ifconfig jdcap0 addm epair4a";
	exec.start = "ifconfig lo0 inet 127.0.0.1/8 up";
	exec.start += "ifconfig epair4b inet 10.79.0.20/24 up";
	exec.start += "route -q add default 10.79.0.1";
}
`
	handStanza = `# 10.79.0.99 was the old one
legacy {
  ip4.addr = 10.79.0.40;
  ip4.addr += "lo1|10.79.0.41/32";
  host.hostname = "legacy.example";
  exec.start = "/bin/sh /etc/rc 192.0.2.7";
}
`
)

func TestAddressUsesReadsStanzasSeedsAndTheARPTable(t *testing.T) {
	a := addressHost(t, "nad_arp_selfset", map[string]string{"web.conf": madeStanza, "jail.conf": handStanza,
		"other.conf": "other {\n  ip4.addr = 10.80.0.5;\n}\n"})
	uses, err := a.AddressUses(context.Background(), netip.MustParsePrefix("10.79.0.0/24"), "jdcap0")
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(filepath.Dir(a.confDir), "jail.conf")
	want := []networks.Use{
		// The route is the gateway's; the service drops it.
		{Address: "10.79.0.20", Kind: networks.UseJail, Name: "web"},
		{Address: "10.79.0.1", Kind: networks.UseJail, Name: "web"},
		{Address: "10.79.0.40", Kind: networks.UseJail, Name: main},
		{Address: "10.79.0.41", Kind: networks.UseJail, Name: main},
		// The ARP table: the jail's two, not the host's permanent one.
		{Address: "10.79.0.5", Kind: networks.UseSeen},
		{Address: "10.79.0.77", Kind: networks.UseSeen},
	}
	if !reflect.DeepEqual(uses, want) {
		t.Errorf("uses =\n%+v\nwant\n%+v", uses, want)
	}
}

func TestAddressUsesReadsAGuestsSeed(t *testing.T) {
	a := addressHost(t, "nad_arp_quiet", nil)
	uses, err := a.AddressUses(context.Background(), netip.MustParsePrefix("10.77.0.0/24"), "jdcap0")
	if err != nil {
		t.Fatal(err)
	}
	// The seed's address, and not its nameserver or route, which are the
	// gateway's.
	want := []networks.Use{{Address: "10.77.0.10", Kind: networks.UseGuest, Name: "jd-cap-made"}}
	if !reflect.DeepEqual(uses, want) {
		t.Errorf("uses = %+v", uses)
	}
}

func TestAddressUsesRefusesABridgeItCannotRead(t *testing.T) {
	a := addressHost(t, "nad_arp_quiet", nil)
	_, err := a.AddressUses(context.Background(), netip.MustParsePrefix("10.79.0.0/24"), "jdnope0")
	if err == nil {
		t.Fatal("a bridge arp cannot read was answered with nothing in use")
	}
}

func TestParseARPLeavesOutThePermanentAndTheIncomplete(t *testing.T) {
	subnet := netip.MustParsePrefix("10.79.0.0/24")
	if uses := parseARP(captured(t, "nad_arp_quiet"), subnet); len(uses) != 0 {
		t.Errorf("a table with the host's own entry alone = %+v", uses)
	}
	got := parseARP(captured(t, "nad_arp_talked")+
		"? (10.79.0.9) at (incomplete) on jdcap0 expired [bridge]\n"+
		"? (10.80.0.9) at 58:9c:fc:10:56:47 on jdcap0 expires in 20 seconds [bridge]\n", subnet)
	if want := []networks.Use{{Address: "10.79.0.5", Kind: networks.UseSeen}}; !reflect.DeepEqual(got, want) {
		t.Errorf("uses = %+v", got)
	}
}

func TestAJailsAddressIsReadFromItsStanza(t *testing.T) {
	list, _ := parseJailConf(madeStanza, nil)
	if len(list) != 1 || list[0].IP != "10.79.0.20" {
		t.Errorf("jails = %+v", list)
	}
	// ip4.addr, where there is one, is what the stanza says.
	list, _ = parseJailConf(strings.Replace(madeStanza, "vnet;", "ip4.addr = 192.0.2.1;", 1), nil)
	if list[0].IP != "192.0.2.1" {
		t.Errorf("ip = %q", list[0].IP)
	}
	// A running VNET jail keeps its stanza's address, as jls has none for
	// it; one jls has an address for is answered with jls's.
	configured, _ := parseJailConf(madeStanza, nil)
	merged := mergeJails(configured, []jails.Jail{{JID: "3", Name: "web", Status: jails.JailStatusRunning}})
	if merged[0].IP != "10.79.0.20" || merged[0].JID != "3" {
		t.Errorf("merged = %+v", merged[0])
	}
	merged = mergeJails(configured, []jails.Jail{{JID: "3", Name: "web", Status: jails.JailStatusRunning, IP: "192.0.2.9"}})
	if merged[0].IP != "192.0.2.9" {
		t.Errorf("merged = %+v", merged[0])
	}
}
