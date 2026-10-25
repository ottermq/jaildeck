package jails

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// --- doubles -----------------------------------------------------------

type stubBridges struct {
	have []string
	err  error
}

func (s *stubBridges) Bridges(context.Context) ([]string, error) { return s.have, s.err }

type stubEpairs struct {
	unit string
	err  error
	call int
}

func (s *stubEpairs) FreeEpairUnit(context.Context) (string, error) {
	s.call++
	return s.unit, s.err
}

type stubEpairCleaner struct {
	removed string
	err     error
	asked   []string
}

func (s *stubEpairCleaner) RemoveJailEpair(_ context.Context, name string) (string, error) {
	s.asked = append(s.asked, name)
	return s.removed, s.err
}

var (
	_ BridgeSource   = (*stubBridges)(nil)
	_ EpairAllocator = (*stubEpairs)(nil)
	_ EpairCleaner   = (*stubEpairCleaner)(nil)
)

func vnetSpec() Spec {
	return Spec{
		Name:     "db1",
		Template: "tank/jails/templates/15.1-RELEASE@base",
		Network: Network{
			Mode: NetworkVNET, Bridge: "bridge0", Private: true,
			IP4: "10.77.0.23/24", Gateway: "10.77.0.1",
		},
	}
}

// vnetService is createService with the three ports a vnet jail needs.
func vnetService(system *createSystem, cloner *stubCloner, config *stubConfig, autostart *stubAutostart,
	logger *fakeLogger, bridges *stubBridges, epairs *stubEpairs, cleaner *stubEpairCleaner) *JailService {
	return NewJailService(Dependencies{
		System: system, Audit: logger,
		RootCloner: cloner, Config: config, Autostart: autostart,
		Bridges: bridges, Epairs: epairs, Epair: cleaner,
	})
}

func vnetParts() (*createSystem, *stubCloner, *stubConfig, *stubAutostart, *fakeLogger,
	*stubBridges, *stubEpairs, *stubEpairCleaner) {
	system := &createSystem{}
	cloner := &stubCloner{root: clonedRoot()}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf", system: system}
	return system, cloner, config, &stubAutostart{}, &fakeLogger{},
		&stubBridges{have: []string{"bridge0", "bridge1"}},
		&stubEpairs{unit: "epair7"}, &stubEpairCleaner{}
}

// --- the stanza, measured against a real host --------------------------

// Every assertion here was measured on isengard, 15.1-RELEASE-p5, before
// this generator was written. The stanza it produces is the stanza that
// started a working VNET jail there.
func TestVNETStanza(t *testing.T) {
	got, err := vnetSpec().Stanza(Placement{
		Path: "/usr/local/jails/containers/db1", Epair: "epair7"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}

	want := []string{
		// The jail end is what moves into the jail's own network stack.
		`vnet.interface = "epair7b";`,
		// An explicit unit, because `ifconfig epair create` prints only the
		// "a" end and the peer's name has to be known before the jail runs.
		`exec.prestart = "ifconfig epair7 create";`,
		// The description is the only thing that identifies the interface as
		// this jail's, and is what the cleanup matches on.
		`exec.prestart += "ifconfig epair7a descr jail:db1 up";`,
		`exec.prestart += "ifconfig bridge0 addm epair7a";`,
		`exec.prestart += "ifconfig bridge0 private epair7a";`,
		// A VNET jail's loopback starts down, which breaks anything that
		// talks to 127.0.0.1 -- including much of what /etc/rc starts.
		`exec.start = "ifconfig lo0 inet 127.0.0.1/8 up";`,
		`exec.start += "ifconfig epair7b inet 10.77.0.23/24 up";`,
		`exec.start += "route -q add default 10.77.0.1";`,
		// rc runs last, so services bind to an interface that is already up.
		`exec.start += "/bin/sh /etc/rc";`,
		// Destroying the host end takes the jail end and the bridge
		// membership with it, measured -- so there is no deletem to fail.
		`exec.poststop = "ifconfig epair7a destroy";`,
	}
	for _, line := range want {
		if !strings.Contains(got, line) {
			t.Errorf("stanza is missing:\n  %s\ngot:\n%s", line, got)
		}
	}

	// ip4.addr, ip4, ip6 and interface are an alias jail's parameters and are
	// invalid with vnet; jail(8) refuses the stanza outright if they appear.
	// Matched as whole settings: "interface =" on its own also matches the
	// vnet.interface line this stanza is required to have.
	for _, line := range strings.Split(got, "\n") {
		setting, _, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		if slices.Contains([]string{"ip4.addr", "ip4", "ip6", "interface"}, setting) {
			t.Errorf("stanza sets %q, which jail(8) refuses alongside vnet:\n%s", setting, got)
		}
	}
	// One exec.start, not two: the non-vnet branch sets it to /etc/rc alone,
	// and both would mean rc running before the address was set.
	if n := strings.Count(got, `exec.start = `); n != 1 {
		t.Errorf("stanza has %d `exec.start =` lines, want exactly 1:\n%s", n, got)
	}

	// The order inside exec.start is the point of writing it out by hand.
	// /etc/rc starts the jail's services, and a service that binds to the
	// jail's address finds nothing there if rc ran first.
	assertOrder(t, got, []string{
		`exec.start = "ifconfig lo0`,
		`exec.start += "ifconfig epair7b inet`,
		`exec.start += "route -q add default`,
		`exec.start += "/bin/sh /etc/rc"`,
	})
	// And the host side is built before the jail is told to use it.
	assertOrder(t, got, []string{
		`exec.prestart = "ifconfig epair7 create"`,
		`exec.prestart += "ifconfig bridge0 addm epair7a"`,
		`exec.start = "ifconfig lo0`,
	})
}

// assertOrder checks that each fragment appears after the one before it.
func assertOrder(t *testing.T, content string, fragments []string) {
	t.Helper()
	previous, at := "", -1
	for _, fragment := range fragments {
		found := strings.Index(content, fragment)
		if found < 0 {
			t.Errorf("stanza is missing:\n  %s\ngot:\n%s", fragment, content)
			return
		}
		if found < at {
			t.Errorf("%q comes before %q, and has to come after:\n%s", fragment, previous, content)
			return
		}
		previous, at = fragment, found
	}
}

func TestVNETStanzaOmitsWhatWasNotAskedFor(t *testing.T) {
	spec := vnetSpec()
	spec.Network.Private = false
	spec.Network.IP4 = ""
	spec.Network.Gateway = ""

	got, err := spec.Stanza(Placement{Path: "/p", Epair: "epair3"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}
	// lo0 still gets an address -- that is not the jail's, and a vnet jail
	// whose loopback is down is broken whatever else it was asked for.
	for _, absent := range []string{"private", "epair3b inet", "route"} {
		if strings.Contains(got, absent) {
			t.Errorf("stanza mentions %q for a spec that did not ask for it:\n%s", absent, got)
		}
	}
	// Still a working jail: it joins the bridge and brings up its loopback.
	for _, line := range []string{
		`exec.prestart += "ifconfig bridge0 addm epair3a";`,
		`exec.start = "ifconfig lo0 inet 127.0.0.1/8 up";`,
	} {
		if !strings.Contains(got, line) {
			t.Errorf("stanza is missing:\n  %s\ngot:\n%s", line, got)
		}
	}
}

// Both ends come from one field, so they cannot disagree about the unit.
func TestVNETStanzaRefusesAnUnreservedEpair(t *testing.T) {
	_, err := vnetSpec().Stanza(Placement{Path: "/p"})
	if err == nil {
		t.Fatal("want an error for a vnet stanza with no epair")
	}
	if !strings.Contains(err.Error(), "no epair was reserved") {
		t.Errorf("message = %q", err)
	}
}

// --- creating one ------------------------------------------------------

func TestCreateVNETJail(t *testing.T) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)

	result, err := service.Create(context.Background(), vnetSpec())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Epair != "epair7a" {
		t.Errorf("Epair = %q, want the host end epair7a", result.Epair)
	}
	if !result.Running {
		t.Error("the jail is not running")
	}
	if epairs.call != 1 {
		t.Errorf("FreeEpairUnit called %d times, want 1", epairs.call)
	}
	// Nothing was cleaned up: the start was confirmed.
	if len(cleaner.asked) != 0 {
		t.Errorf("the epair was swept after a successful start: %v", cleaner.asked)
	}
	if !strings.Contains(config.written["db1"], "epair7b") {
		t.Errorf("the reserved unit did not reach the stanza:\n%s", config.written["db1"])
	}
}

func TestCreateVNETRefusesABridgeTheHostDoesNotHave(t *testing.T) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	spec := vnetSpec()
	spec.Network.Bridge = "bridge9"
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)

	result, err := service.Create(context.Background(), spec)
	if got := appCode(t, err); got != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%v)", got, err)
	}
	// The ones that do exist are named, because "no such bridge" without
	// them is a message an operator cannot act on.
	if !strings.Contains(err.Error(), "bridge0, bridge1") {
		t.Errorf("message = %q, want it to list the bridges the host has", err)
	}
	if result.FailedAt != StepCheck {
		t.Errorf("FailedAt = %q, want %q", result.FailedAt, StepCheck)
	}
	// Refused before anything exists. A bridge typo must not cost a dataset.
	if len(cloner.cloned) != 0 || epairs.call != 0 {
		t.Errorf("a bad bridge got past the check: cloned %v, allocations %d", cloner.cloned, epairs.call)
	}
}

func TestCreateVNETWithoutThePortsIs501(t *testing.T) {
	for _, test := range []struct {
		name  string
		drop  func(*Dependencies)
		other bool
	}{
		{name: "no allocator", drop: func(d *Dependencies) { d.Epairs = nil }},
		{name: "no cleaner", drop: func(d *Dependencies) { d.Epair = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
			deps := Dependencies{
				System: system, Audit: logger,
				RootCloner: cloner, Config: config, Autostart: autostart,
				Bridges: bridges, Epairs: epairs, Epair: cleaner,
			}
			test.drop(&deps)

			_, err := NewJailService(deps).Create(context.Background(), vnetSpec())
			if got := appCode(t, err); got != http.StatusNotImplemented {
				t.Fatalf("want 501, got %d (%v)", got, err)
			}
			if len(cloner.cloned) != 0 {
				t.Errorf("a dataset was cloned for a jail that cannot be built: %v", cloner.cloned)
			}
		})
	}
}

// Another mode must not need the vnet ports at all.
func TestCreateWithoutTheVNETPortsStillBuildsAnAliasJail(t *testing.T) {
	system, cloner, config, autostart, logger, _, _, _ := vnetParts()
	service := NewJailService(Dependencies{
		System: system, Audit: logger,
		RootCloner: cloner, Config: config, Autostart: autostart,
	})

	result, err := service.Create(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Epair != "" {
		t.Errorf("Epair = %q, want none for an alias jail", result.Epair)
	}
}

// --- the leak, which is the whole reason this is careful ---------------

// jail(8) does not run exec.poststop for a start that failed, and nothing
// afterwards will. The orphan blocks every retry, so Jail Deck takes it.
func TestCreateVNETDestroysTheEpairWhenTheStartCannotBeConfirmed(t *testing.T) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	system.startErr = common.NewAppError(http.StatusInternalServerError, "jail db1 did not come up")
	cleaner.removed = "epair7a"
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)

	result, err := service.Create(context.Background(), vnetSpec())
	if err == nil {
		t.Fatal("want the start's error")
	}
	if result.FailedAt != StepStart {
		t.Errorf("FailedAt = %q, want %q", result.FailedAt, StepStart)
	}
	if !slices.Equal(cleaner.asked, []string{"db1"}) {
		t.Errorf("cleaner asked %v, want the jail swept exactly once", cleaner.asked)
	}
	if !result.EpairRemoved {
		t.Error("EpairRemoved is false after the epair was destroyed")
	}
	// The start's own message survives: the cleanup is a footnote, not the
	// headline.
	if !strings.Contains(err.Error(), "did not come up") {
		t.Errorf("error = %q, want the start's reason", err)
	}
	// Everything else is reported, not undone.
	if result.Root == "" || result.Config == "" {
		t.Errorf("the half-created jail is not reported: %+v", result)
	}
}

// A cleanup that fails names the orphan. A leaked epair somebody knows about
// is a minute's work; a silent one is a jail that will never start again.
func TestCreateVNETNamesTheOrphanWhenCleanupFails(t *testing.T) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	system.startErr = common.NewAppError(http.StatusInternalServerError, "jail db1 did not come up")
	cleaner.err = errors.New("ifconfig: interface epair7a does not exist")
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)

	result, err := service.Create(context.Background(), vnetSpec())
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"did not come up", "epair7a", "bridge0", "ifconfig epair7a destroy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
	if result.EpairRemoved {
		t.Error("EpairRemoved is true after the cleanup failed")
	}
}

// Only a vnet jail has an epair; sweeping any other mode would be a command
// run for nothing, against an interface that cannot exist.
func TestCreateDoesNotSweepForANonVNETJail(t *testing.T) {
	system, cloner, config, autostart, logger, bridges, epairs, cleaner := vnetParts()
	system.startErr = common.NewAppError(http.StatusInternalServerError, "nope")
	service := vnetService(system, cloner, config, autostart, logger, bridges, epairs, cleaner)

	if _, err := service.Create(context.Background(), validSpec()); err == nil {
		t.Fatal("want the start's error")
	}
	if len(cleaner.asked) != 0 {
		t.Errorf("an alias jail was swept for an epair: %v", cleaner.asked)
	}
}

// --- destroying one ----------------------------------------------------

func TestDestroySweepsALeftoverEpair(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	cleaner := &stubEpairCleaner{removed: "epair7a"}
	service := NewJailService(Dependencies{
		System: system, Audit: logger,
		RootDestroyer: destroyer, ConfigRemover: remover, Autostart: autostart,
		Epair: cleaner,
	})

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if result.Epair != "epair7a" {
		t.Errorf("Epair = %q, want the swept interface named", result.Epair)
	}
	if !result.Destroyed {
		t.Error("the destroy did not finish")
	}
}

// The jail and its dataset are what was asked for. An interface that will
// not go is reported, not allowed to abort the rest.
func TestDestroyCarriesOnWhenTheEpairWillNotGo(t *testing.T) {
	system, destroyer, remover, autostart, logger := destroyable()
	cleaner := &stubEpairCleaner{err: errors.New("device busy")}
	service := NewJailService(Dependencies{
		System: system, Audit: logger,
		RootDestroyer: destroyer, ConfigRemover: remover, Autostart: autostart,
		Epair: cleaner,
	})

	result, err := service.Destroy(context.Background(), "db1", "db1")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Destroyed {
		t.Error("a stranded interface stopped the destroy")
	}
	if !strings.Contains(result.EpairError, "device busy") {
		t.Errorf("EpairError = %q, want the reason named", result.EpairError)
	}
}
