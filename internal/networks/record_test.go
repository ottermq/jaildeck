package networks

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

const recordPath = "/usr/local/etc/jaildeck/pf/networks.json"

// reboot takes every tenant bridge away, as a reboot does (#225).
func (h *host) reboot() { h.bridges = map[string]*TenantBridge{} }

func TestCreatingAndDestroyingKeepTheRecord(t *testing.T) {
	h := readyHost()
	s, _ := service(h)
	ctx := context.Background()
	for _, req := range []CreateNetworkRequest{{Name: "acme", Tenant: "acme"}, {Name: "globex"}} {
		if _, err := s.CreateNetwork(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	rec, found, err := s.readRecord(ctx)
	if err != nil || !found {
		t.Fatalf("record: found %v, %v\n%s", found, err, h.files[recordPath])
	}
	want := []Recorded{
		{Name: "acme", Tenant: "acme", Bridge: "jdn0", Gateway: "10.92.0.1/24"},
		{Name: "globex", Bridge: "jdn1", Gateway: "10.92.1.1/24"},
	}
	if !slices.Equal(rec, want) {
		t.Errorf("record = %+v\nwant     %+v", rec, want)
	}
	if _, err := s.DestroyNetwork(ctx, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	rec, _, _ = s.readRecord(ctx)
	if !slices.Equal(rec, want[1:]) {
		t.Errorf("record after destroying acme = %+v", rec)
	}
}

func TestRestorePutsBackWhatARebootTook(t *testing.T) {
	h := readyHost()
	s, rec := service(h)
	ctx := context.Background()
	for _, req := range []CreateNetworkRequest{{Name: "acme", Tenant: "acme"}, {Name: "globex"}} {
		if _, err := s.CreateNetwork(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	h.reboot()

	list, err := s.Networks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Networks) != 0 || len(list.Missing) != 2 || list.Missing[0].Name != "acme" ||
		list.Missing[0].Subnet != "10.92.0.0/24" || list.Missing[0].Gateway != "10.92.0.1" {
		t.Errorf("after the reboot: %+v", list)
	}

	audited := len(rec.entries)
	done, err := s.Restore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Created) != 2 || len(done.Present) != 0 {
		t.Errorf("restored %+v", done)
	}
	if !slices.Equal(rec.ops()[audited:], oks(OperationCreateNetworkBridge, OperationSetNetworkAddress,
		OperationCreateNetworkBridge, OperationSetNetworkAddress)) {
		t.Errorf("audited %v", rec.ops()[audited:])
	}
	if b := h.bridges["jdn0"]; b == nil || b.Description != "jaildeck:network=acme;tenant=acme" ||
		b.Address != netip.MustParsePrefix("10.92.0.1/24") {
		t.Errorf("jdn0 = %+v", b)
	}
	if b := h.bridges["jdn1"]; b == nil || b.Description != "jaildeck:network=globex" {
		t.Errorf("jdn1 = %+v", b)
	}

	// Again: nothing to do.
	audited = len(rec.entries)
	done, err = s.Restore(ctx)
	if err != nil || len(done.Created) != 0 || len(done.Present) != 2 || len(rec.entries) != audited {
		t.Errorf("second restore: %+v, %v, audited %v", done, err, rec.ops()[audited:])
	}
}

// A host whose networks were made before the record learns them at start.
func TestCheckRecordAdoptsTheHostsNetworks(t *testing.T) {
	h := readyHost()
	h.bridges["jdn0"] = &TenantBridge{Name: "jdn0", Description: "jaildeck:network=acme;tenant=acme",
		Address: netip.MustParsePrefix("10.92.0.1/24")}
	h.bridges["jdn3"] = &TenantBridge{Name: "jdn3", Description: "someone else's"}
	s, _ := service(h)
	ctx := context.Background()
	s.CheckRecord(ctx)
	rec, found, err := s.readRecord(ctx)
	if err != nil || !found || !slices.Equal(rec, []Recorded{{Name: "acme", Tenant: "acme", Bridge: "jdn0", Gateway: "10.92.0.1/24"}}) {
		t.Errorf("record = %+v, found %v, %v", rec, found, err)
	}

	// No networks and no record: no file either.
	h2 := readyHost()
	s2, _ := service(h2)
	s2.CheckRecord(ctx)
	if _, ok := h2.files[recordPath]; ok {
		t.Error("a host without networks got a record")
	}
}

// A missing network keeps its bridge and subnet: a new one does not take
// them, and does not take its name.
func TestAMissingNetworkKeepsItsPlace(t *testing.T) {
	h := readyHost()
	s, _ := service(h)
	ctx := context.Background()
	if _, err := s.CreateNetwork(ctx, CreateNetworkRequest{Name: "acme"}); err != nil {
		t.Fatal(err)
	}
	h.reboot()
	// vm-bhyve keeps the switch in its configuration across the reboot.
	h.switches = slices.DeleteFunc(h.switches, func(sw Switch) bool { return sw.Name == "acme" })
	_, err := s.CreateNetwork(ctx, CreateNetworkRequest{Name: "acme"})
	wantAppError(t, err, http.StatusConflict, "networks restore")
	n, err := s.CreateNetwork(ctx, CreateNetworkRequest{Name: "globex"})
	if err != nil {
		t.Fatal(err)
	}
	if n.Bridge != "jdn1" || n.Subnet != "10.92.1.0/24" {
		t.Errorf("the new network took %s %s", n.Bridge, n.Subnet)
	}
}

func TestDestroyingAMissingNetwork(t *testing.T) {
	h := readyHost()
	s, rec := service(h)
	ctx := context.Background()
	if _, err := s.CreateNetwork(ctx, CreateNetworkRequest{Name: "acme"}); err != nil {
		t.Fatal(err)
	}
	h.reboot()
	audited := len(rec.entries)
	if _, err := s.DestroyNetwork(ctx, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rec.ops()[audited:], oks(OperationDestroySwitch, OperationWriteNetworkRecord, OperationWriteAnchor)) {
		t.Errorf("audited %v", rec.ops()[audited:])
	}
	if r, _, _ := s.readRecord(ctx); len(r) != 0 {
		t.Errorf("record = %+v", r)
	}
}

// A create whose record cannot be written takes the network back.
func TestCreateUndoesWhenTheRecordFails(t *testing.T) {
	h := readyHost()
	h.fail = map[string]error{"record": errors.New("read-only file system")}
	s, _ := service(h)
	_, err := s.CreateNetwork(context.Background(), CreateNetworkRequest{Name: "acme"})
	if err == nil || !strings.Contains(err.Error(), "record the network") {
		t.Fatalf("err = %v", err)
	}
	if len(h.bridges) != 0 {
		t.Errorf("bridges left: %v", h.bridges)
	}
}

// Restore leaves a bridge that is there with another description alone, and
// says so; the other networks are still put back.
func TestRestoreLeavesAForeignBridgeAlone(t *testing.T) {
	h := readyHost()
	h.files[recordPath] = `{"version":1,"networks":[` +
		`{"name":"acme","bridge":"jdn0","gateway":"10.92.0.1/24"},` +
		`{"name":"globex","bridge":"jdn1","gateway":"10.92.1.1/24"}]}`
	h.bridges["jdn0"] = &TenantBridge{Name: "jdn0", Description: "someone else's"}
	s, _ := service(h)
	done, err := s.Restore(context.Background())
	if err == nil || len(done.Problems) != 1 || !strings.Contains(done.Problems[0], "someone else's") ||
		len(done.Created) != 1 || done.Created[0].Bridge != "jdn1" {
		t.Errorf("restore: %+v, %v", done, err)
	}
	if h.bridges["jdn0"].Description != "someone else's" {
		t.Error("the foreign bridge was changed")
	}
}

// An entry that would put something odd on ifconfig's command line refuses
// the whole record.
func TestABadRecordIsRefused(t *testing.T) {
	for _, entry := range []string{
		`{"name":"acme","bridge":"em0","gateway":"10.92.0.1/24"}`,
		`{"name":"acme","bridge":"jdn0","gateway":"10.92.0.1/24 up"}`,
		`{"name":"Acme;rm","bridge":"jdn0","gateway":"10.92.0.1/24"}`,
	} {
		h := readyHost()
		h.files[recordPath] = `{"version":1,"networks":[` + entry + `]}`
		s, _ := service(h)
		if _, err := s.Restore(context.Background()); err == nil {
			t.Errorf("%s: restored", entry)
		}
		if len(h.bridges) != 0 {
			t.Errorf("%s: made %v", entry, h.bridges)
		}
	}
}
