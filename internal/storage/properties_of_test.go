package storage

import (
	"context"
	"reflect"
	"testing"
)

// One zfs get for every guest, and only the datasets asked for answer: -r
// reaches each guest's disk too, which inherits its labels rather than
// setting its own, and a native property is never a label (JD-044).
func TestUserPropertiesOf(t *testing.T) {
	system := &stubSystem{properties: []Property{
		{Dataset: "tank/vm/web", Name: "otter:tenant", Value: "acme"},
		{Dataset: "tank/vm/web", Name: "compression", Value: "lz4"},
		{Dataset: "tank/vm/web/disk0", Name: "otter:disk", Value: "x"},
		{Dataset: "tank/vm/db", Name: "otter:tier", Value: "data"},
		{Dataset: "tank/vm/other", Name: "otter:tenant", Value: "beta"},
	}}
	got, err := NewService(system, &fakeLogger{}, "tank").
		UserPropertiesOf(context.Background(), []string{"tank/vm/web", "tank/vm/db", "tank/vm/bare"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"tank/vm/web": {"otter:tenant": "acme"},
		"tank/vm/db":  {"otter:tier": "data"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if !reflect.DeepEqual(system.roots, [][]string{{"tank/vm/web", "tank/vm/db", "tank/vm/bare"}}) {
		t.Errorf("asked %q, want one read of the three", system.roots)
	}
}
