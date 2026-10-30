package freebsd

import (
	"os"
	"reflect"
	"testing"
)

// The datastores as vm-bhyve listed them on the lab host (JD-044): a guest's
// dataset is the datastore's plus its name, and a directory datastore has
// none, so its guests have nowhere to keep a label.
func TestParseDatastores(t *testing.T) {
	out, err := os.ReadFile(fixture("isengard-induced", "vml_datastore", "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parseDatastores(string(out)), map[string]string{"default": "tank/vm"}; !reflect.DeepEqual(got, want) {
		t.Errorf("parsed %v, want %v", got, want)
	}

	// A second datastore that is a plain directory, in vm-bhyve's layout.
	mixed := string(out) + "plain           directory   /usr/local/vm2            -\n"
	if got := parseDatastores(mixed); got["plain"] != "" || got["default"] != "tank/vm" || len(got) != 2 {
		t.Errorf("parsed %v, want plain with no dataset beside default", got)
	}
}
