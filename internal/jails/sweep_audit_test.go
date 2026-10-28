package jails

import (
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
)

// wantSweep asserts the one audit entry a sweep wrote: the operation, its
// command, and whether it worked. want nil asserts there is none.
func wantSweep(t *testing.T, logger *fakeLogger, operation string, want *audit.Entry) {
	t.Helper()
	var found []audit.Entry
	for _, e := range logger.entries {
		if e.Operation == operation {
			found = append(found, e)
		}
	}
	if want == nil {
		if len(found) != 0 {
			t.Errorf("%s audited %+v, want nothing: the sweep found nothing to change", operation, found)
		}
		return
	}
	if len(found) != 1 {
		t.Fatalf("%s audited %d times, want once: %+v", operation, len(found), logger.entries)
	}
	if found[0].Command != want.Command || found[0].Success != want.Success || found[0].Target != "db1" {
		t.Errorf("%s entry = %+v, want command %q, success %v, target db1",
			operation, found[0], want.Command, want.Success)
	}
}
