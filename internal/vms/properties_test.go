package vms

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

// stubProperties is the storage domain's side of a guest's labels: what each
// dataset carries, and the writes asked of it.
type stubProperties struct {
	found  map[string]map[string]string
	err    error
	asked  [][]string
	writes []string
}

func (p *stubProperties) UserPropertiesOf(_ context.Context, datasets []string) (map[string]map[string]string, error) {
	p.asked = append(p.asked, datasets)
	return p.found, p.err
}

func (p *stubProperties) SetUserProperties(_ context.Context, dataset string, properties map[string]*string) (map[string]string, error) {
	p.writes = append(p.writes, dataset)
	labels := map[string]string{}
	for name, value := range p.found[dataset] {
		labels[name] = value
	}
	for name, value := range properties {
		if value == nil {
			delete(labels, name)
		} else {
			labels[name] = *value
		}
	}
	return labels, nil
}

// Two guests in the ZFS datastore and one in a directory datastore, which has
// nowhere to keep a label (JD-044).
func labelledHost() (*maker, *stubProperties, *Service) {
	m := newMaker()
	m.listing = Listing{VMBhyve: VMBhyveReady, VMs: []VM{
		{Name: "web", Datastore: "default", State: StateRunning},
		{Name: "db", Datastore: "default", State: StateStopped},
		{Name: "old", Datastore: "plain", State: StateStopped},
	}}
	p := &stubProperties{found: map[string]map[string]string{
		"tank/vm/web": {"otter:tenant": "acme", "otter:tier": "frontend"},
	}}
	s, _ := newMakerService(m)
	s.SetProperties(p)
	return m, p, s
}

// One read for every guest on ZFS, and every guest answers with an object --
// empty rather than null for one with no labels or nowhere to keep them.
func TestListCarriesEachGuestsLabels(t *testing.T) {
	_, p, s := labelledHost()
	listing, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"tank/vm/web", "tank/vm/db"}}; !reflect.DeepEqual(p.asked, want) {
		t.Errorf("asked for %q, want one read of the two ZFS guests", p.asked)
	}
	got := map[string]map[string]string{}
	for _, vm := range listing.VMs {
		if vm.Properties == nil {
			t.Errorf("%s: properties is nil, want an object", vm.Name)
		}
		got[vm.Name] = vm.Properties
	}
	want := map[string]map[string]string{
		"web": {"otter:tenant": "acme", "otter:tier": "frontend"},
		"db":  {},
		"old": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels %v, want %v", got, want)
	}
}

// A failure to read the labels costs the labels, not the listing.
func TestListSurvivesUnreadableLabels(t *testing.T) {
	_, p, s := labelledHost()
	p.err = errors.New("zfs get: I/O error")
	listing, err := s.List(context.Background())
	if err != nil || len(listing.VMs) != 3 {
		t.Fatalf("listing = %+v, %v; want the three guests", listing, err)
	}
	if len(listing.VMs[0].Properties) != 0 {
		t.Errorf("labels = %v, want none when they cannot be read", listing.VMs[0].Properties)
	}
}

func TestGetCarriesTheGuestsLabels(t *testing.T) {
	_, _, s := labelledHost()
	detail, err := s.Get(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Properties["otter:tenant"] != "acme" {
		t.Errorf("properties = %v, want otter:tenant=acme", detail.Properties)
	}
}

// A label is written to <datastore's dataset>/<name>, running or not.
func TestUpdatePropertiesWritesTheGuestsDataset(t *testing.T) {
	_, p, s := labelledHost()
	value := "gold"
	got, err := s.UpdateProperties(context.Background(), "web",
		map[string]*string{"otter:plan": &value, "otter:tier": nil})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.writes, []string{"tank/vm/web"}) {
		t.Errorf("wrote %q, want the guest's own dataset", p.writes)
	}
	if want := map[string]string{"otter:tenant": "acme", "otter:plan": "gold"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels %v, want %v", got, want)
	}
}

func TestUpdatePropertiesRefusals(t *testing.T) {
	value := "x"
	for _, tt := range []struct {
		name, guest string
		want        int
		says        string
	}{
		{name: "no such guest", guest: "nope", want: http.StatusNotFound},
		{name: "not a guest name", guest: "-bad", want: http.StatusBadRequest},
		{name: "a directory datastore", guest: "old", want: http.StatusConflict, says: "not on ZFS"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, p, s := labelledHost()
			_, err := s.UpdateProperties(context.Background(), tt.guest, map[string]*string{"otter:x": &value})
			wantAppError(t, err, tt.want, tt.says)
			if len(p.writes) != 0 {
				t.Errorf("wrote %q, want nothing", p.writes)
			}
		})
	}
}

// A service built without the storage domain says so rather than panicking.
func TestUpdatePropertiesWithoutAStore(t *testing.T) {
	m := newMaker()
	m.listing = Listing{VMBhyve: VMBhyveReady, VMs: []VM{{Name: "web", Datastore: "default"}}}
	s, _ := newMakerService(m)
	value := "x"
	_, err := s.UpdateProperties(context.Background(), "web", map[string]*string{"otter:x": &value})
	wantAppError(t, err, http.StatusNotImplemented, "storage access")
}
