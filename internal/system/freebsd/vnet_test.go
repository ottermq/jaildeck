package freebsd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// vnetAdapter records every command it was asked to run and answers the
// ones the test set up.
type vnetAdapter struct {
	*Adapter
	ran    []string
	answer map[string]string
	fail   map[string]error
}

func newVNETAdapter(t *testing.T) *vnetAdapter {
	t.Helper()
	v := &vnetAdapter{answer: map[string]string{}, fail: map[string]error{}}
	v.Adapter = NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		line := cmd.String()
		v.ran = append(v.ran, line)
		if err, ok := v.fail[line]; ok {
			return system.CommandResult{}, err
		}
		return system.CommandResult{Stdout: v.answer[line]}, nil
	}))
	v.confDir = filepath.Join(t.TempDir(), "jail.conf.d")
	v.confFile = filepath.Join(t.TempDir(), "jail.conf")
	return v
}

func (v *vnetAdapter) writeConf(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(v.confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.confDir, name+".conf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- allocation --------------------------------------------------------

func TestFreeEpairUnitOnAnIdleHost(t *testing.T) {
	adapter := newVNETAdapter(t)

	got, err := adapter.FreeEpairUnit(context.Background())
	if err != nil {
		t.Fatalf("FreeEpairUnit: %v", err)
	}
	if got != "epair0" {
		t.Errorf("unit = %q, want epair0", got)
	}
}

// Both halves of the question matter: interfaces answer for jails that are
// running, configurations for jails that are not. A unit handed out from
// under a stopped jail would work until that jail was started.
func TestFreeEpairUnitSkipsWhatIsUsedAndWhatIsClaimed(t *testing.T) {
	adapter := newVNETAdapter(t)
	// Running: epair0 and epair2 exist on the host.
	adapter.answer["ifconfig -g epair"] = "epair0a\nepair0b\nepair2a\nepair2b\n"
	// Stopped, but its stanza names epair1 and will create it on start.
	adapter.writeConf(t, "web", `web {
	vnet;
	vnet.interface = "epair1b";
	exec.prestart = "ifconfig epair1 create";
	exec.poststop = "ifconfig epair1a destroy";
}
`)

	got, err := adapter.FreeEpairUnit(context.Background())
	if err != nil {
		t.Fatalf("FreeEpairUnit: %v", err)
	}
	if got != "epair3" {
		t.Errorf("unit = %q, want epair3 -- 0 and 2 are up, 1 is claimed by a stopped jail", got)
	}
}

// /etc/jail.conf belongs to the operator and Jail Deck never writes it, but a
// VNET jail declared there still owns its unit.
func TestFreeEpairUnitReadsTheSharedFileToo(t *testing.T) {
	adapter := newVNETAdapter(t)
	if err := os.WriteFile(adapter.confFile, []byte("legacy {\n\texec.prestart = \"ifconfig epair0 create\";\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := adapter.FreeEpairUnit(context.Background())
	if err != nil {
		t.Fatalf("FreeEpairUnit: %v", err)
	}
	if got != "epair1" {
		t.Errorf("unit = %q, want epair1 -- epair0 is claimed by /etc/jail.conf", got)
	}
}

// A host with no /etc/jail.conf is the layout Jail Deck writes (JD-030), not
// an error.
func TestFreeEpairUnitWithNoSharedFile(t *testing.T) {
	adapter := newVNETAdapter(t)
	if _, err := os.Stat(adapter.confFile); !os.IsNotExist(err) {
		t.Fatalf("the test assumes no shared file: %v", err)
	}
	if _, err := adapter.FreeEpairUnit(context.Background()); err != nil {
		t.Errorf("FreeEpairUnit: %v", err)
	}
}

func TestFreeEpairUnitFailsWhenTheHostCannotBeRead(t *testing.T) {
	adapter := newVNETAdapter(t)
	adapter.fail["ifconfig -g epair"] = errors.New("ifconfig: socket: Protocol not supported")

	if _, err := adapter.FreeEpairUnit(context.Background()); err == nil {
		t.Fatal("want an error rather than a guess at a free unit")
	}
}

// --- cleanup -----------------------------------------------------------

const ifconfigAll = `em0: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> metric 0 mtu 1500
	options=481249b<RXCSUM,TXCSUM,VLAN_MTU>
	ether 00:00:5e:00:53:01
	inet 192.0.2.5 netmask 0xffffff00 broadcast 192.0.2.255
bridge0: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> metric 0 mtu 1500
	ether 58:9c:fc:10:d4:69
	member: epair7a flags=943<LEARNING,DISCOVER,PRIVATE>
epair7a: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> metric 0 mtu 1500
	description: jail:db1
	ether 58:9c:fc:10:09:51
	groups: epair
epair9a: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST> metric 0 mtu 1500
	description: jail:web
	groups: epair
lo0: flags=1008049<UP,LOOPBACK,RUNNING,MULTICAST> metric 0 mtu 16384
	inet 127.0.0.1 netmask 0xff000000
`

func TestRemoveJailEpair(t *testing.T) {
	adapter := newVNETAdapter(t)
	adapter.answer["ifconfig -a"] = ifconfigAll

	got, err := adapter.RemoveJailEpair(context.Background(), "db1")
	if err != nil {
		t.Fatalf("RemoveJailEpair: %v", err)
	}
	if got != "epair7a" {
		t.Errorf("removed %q, want epair7a", got)
	}
	// One command to read the whole host, one to destroy. No deletem:
	// destroying the host end takes the bridge membership and the jail end
	// with it, so a second command could only add a way to fail.
	want := []string{"ifconfig -a", "ifconfig epair7a destroy"}
	if !slices.Equal(adapter.ran, want) {
		t.Errorf("commands = %v, want %v", adapter.ran, want)
	}
}

// Nothing to clean up is the ordinary case: a jail that stopped normally
// took its own epair away through exec.poststop.
func TestRemoveJailEpairFindsNothing(t *testing.T) {
	adapter := newVNETAdapter(t)
	adapter.answer["ifconfig -a"] = ifconfigAll

	got, err := adapter.RemoveJailEpair(context.Background(), "nosuchjail")
	if err != nil {
		t.Fatalf("RemoveJailEpair: %v", err)
	}
	if got != "" {
		t.Errorf("removed %q, want nothing", got)
	}
	if slices.Contains(adapter.ran, "ifconfig nosuchjail destroy") || len(adapter.ran) != 1 {
		t.Errorf("commands = %v, want only the read", adapter.ran)
	}
}

// The description is the only thing that says whose the interface is. A
// sweep matching on a name pattern could destroy an epair belonging to
// something else on the host.
func TestRemoveJailEpairMatchesTheDescriptionNotTheName(t *testing.T) {
	adapter := newVNETAdapter(t)
	adapter.answer["ifconfig -a"] = `epair7a: flags=8843 metric 0 mtu 1500
	description: somebody-elses-epair
	groups: epair
db1: flags=8843 metric 0 mtu 1500
	groups: epair
`

	got, err := adapter.RemoveJailEpair(context.Background(), "db1")
	if err != nil {
		t.Fatalf("RemoveJailEpair: %v", err)
	}
	if got != "" {
		t.Errorf("removed %q; neither interface says it belongs to db1", got)
	}
}

// A jail whose name is a prefix of another's must not take its interface.
func TestRemoveJailEpairDoesNotMatchAPrefix(t *testing.T) {
	adapter := newVNETAdapter(t)
	adapter.answer["ifconfig -a"] = ifconfigAll

	got, err := adapter.RemoveJailEpair(context.Background(), "db")
	if err != nil {
		t.Fatalf("RemoveJailEpair: %v", err)
	}
	if got != "" {
		t.Errorf("removed %q for jail \"db\"; jail:db1 is a different jail", got)
	}
}

func TestRemoveJailEpairReportsAFailedDestroy(t *testing.T) {
	adapter := newVNETAdapter(t)
	adapter.answer["ifconfig -a"] = ifconfigAll
	adapter.fail["ifconfig epair7a destroy"] = errors.New("ifconfig: SIOCIFDESTROY: Operation not permitted")

	_, err := adapter.RemoveJailEpair(context.Background(), "db1")
	if err == nil {
		t.Fatal("want an error so the orphan can be named to the operator")
	}
	if !strings.Contains(err.Error(), "epair7a") {
		t.Errorf("error = %q, want the interface named", err)
	}
}

// --- the round trip ----------------------------------------------------

// The generated VNET stanza has to survive Jail Deck's own parser, or the
// jail exists on disk and appears in no listing (JD-004). The non-vnet
// stanza is covered by TestWriteConfigRoundTripsThroughTheParser; this is
// the branch with ten exec lines in it.
func TestVNETStanzaRoundTripsThroughTheParser(t *testing.T) {
	adapter := newVNETAdapter(t)
	spec := jails.Spec{
		Name:     "db1",
		Hostname: "db1",
		Template: "tank/jails/templates/15.1-RELEASE@base",
		Network: jails.Network{
			Mode: jails.NetworkVNET, Bridge: "bridge0", Private: true,
			IP4: "10.77.0.23/24", Gateway: "10.77.0.1",
		},
	}
	content, err := spec.Stanza(jails.Placement{
		Path: "/usr/local/jails/containers/db1", Epair: "epair7"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := adapter.WriteConfig(context.Background(), "db1", content); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	configured, err := adapter.listConfiguredJails()
	if err != nil {
		t.Fatalf("listConfiguredJails: %v", err)
	}
	if len(configured) != 1 {
		t.Fatalf("parsed %d jails from the stanza just written, want 1: %+v", len(configured), configured)
	}
	jail := configured[0]
	if jail.Name != "db1" {
		t.Errorf("name = %q, want db1", jail.Name)
	}
	if jail.Path != "/usr/local/jails/containers/db1" {
		t.Errorf("path = %q", jail.Path)
	}
	if jail.Hostname != "db1" {
		t.Errorf("hostname = %q", jail.Hostname)
	}
	// A VNET jail sets no ip4.addr, so neither the configuration nor jls
	// reports an address for it. The address lives in the jail's own stack.
	if jail.IP != "" {
		t.Errorf("ip = %q, want empty: a vnet jail has no ip4.addr to read", jail.IP)
	}

	// And the unit the stanza claims is found again, which is what stops the
	// next jail being handed the same one.
	unit, err := adapter.FreeEpairUnit(context.Background())
	if err != nil {
		t.Fatalf("FreeEpairUnit: %v", err)
	}
	if unit == "epair7" {
		t.Error("the unit this stanza claims was handed out again")
	}
}
