package storage

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

func withJail() *stubSystem {
	return &stubSystem{
		pools: []string{"tank"},
		datasets: []Dataset{
			{Name: "tank", Mountpoint: "none"},
			{Name: "tank/jails/containers/ottermq", Mountpoint: "/usr/local/jails/containers/ottermq"},
		},
		properties: []Property{
			{Dataset: "tank/jails/containers/ottermq", Name: "otter:instance", Value: "mq-01"},
			{Dataset: "tank/jails/containers/ottermq", Name: "atime", Value: "off"},
		},
	}
}

func appErrCode(t *testing.T, err error) int {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error %v is not an *common.AppError", err)
	}
	return appErr.Code
}

func TestEnsureDatasetCreates(t *testing.T) {
	system := withJail()
	logger := &fakeLogger{}
	service := NewService(system, logger, "tank")

	change, err := service.EnsureDataset(context.Background(), "tank/jails/containers/ottermq/data", map[string]string{
		"refquota":   "10G",
		"recordsize": "16K",
		"mountpoint": "/usr/local/jails/containers/ottermq/data",
	})
	if err != nil {
		t.Fatalf("EnsureDataset() error = %v", err)
	}
	if !change.Created {
		t.Errorf("Created = false, want true for a dataset that was absent")
	}
	want := "zfs create -o mountpoint=/usr/local/jails/containers/ottermq/data -o recordsize=16K -o refquota=10G tank/jails/containers/ottermq/data"
	if len(system.ran) != 1 || system.ran[0] != want {
		t.Errorf("ran %q, want one %q", system.ran, want)
	}

	// Audited, with the command, because a write nobody can account for later
	// is the thing the audit log exists to prevent (JD-011).
	if len(logger.entries) != 1 {
		t.Fatalf("wrote %d audit entries, want 1", len(logger.entries))
	}
	entry := logger.entries[0]
	if entry.Operation != OperationCreateDataset || entry.Target != "tank/jails/containers/ottermq/data" || !entry.Success {
		t.Errorf("entry = %+v, want a successful create-dataset naming the dataset", entry)
	}
	if entry.Command != want {
		t.Errorf("entry command = %q, want %q", entry.Command, want)
	}
}

// The second call of a client that timed out on the first. It must not fail,
// and it must say the dataset was not created this time (JD-016).
func TestEnsureDatasetIsIdempotent(t *testing.T) {
	system := withJail()
	service := NewService(system, &fakeLogger{}, "tank")
	properties := map[string]string{"refquota": "10G", "mountpoint": "/usr/local/jails/containers/ottermq/data"}

	first, err := service.EnsureDataset(context.Background(), "tank/jails/containers/ottermq/data", properties)
	if err != nil || !first.Created {
		t.Fatalf("first EnsureDataset() = %+v, %v; want created", first, err)
	}
	second, err := service.EnsureDataset(context.Background(), "tank/jails/containers/ottermq/data", properties)
	if err != nil {
		t.Fatalf("second EnsureDataset() error = %v", err)
	}
	if second.Created {
		t.Errorf("second call reported Created, want false")
	}
	if second.Conflict != nil {
		t.Errorf("second call reported a conflict for the same request: %+v", second.Conflict)
	}
	// The mountpoint matched, so it is dropped from the update rather than
	// refused -- and refquota is applied again, which ZFS treats as a no-op.
	if !reflect.DeepEqual(second.Applied, map[string]string{"refquota": "10G"}) {
		t.Errorf("Applied = %v, want only refquota", second.Applied)
	}
}

// Asking for a dataset at a different mountpoint than the one that exists is
// not a change this API makes: it means the caller has a different dataset in
// mind, and both values go back so they can see which.
func TestEnsureDatasetReportsAMountpointConflict(t *testing.T) {
	system := withJail()
	service := NewService(system, &fakeLogger{}, "tank")

	change, err := service.EnsureDataset(context.Background(), "tank/jails/containers/ottermq", map[string]string{
		"mountpoint": "/srv/somewhere-else",
		"refquota":   "10G",
	})
	if err != nil {
		t.Fatalf("EnsureDataset() error = %v", err)
	}
	if change.Conflict == nil {
		t.Fatalf("change = %+v, want a conflict", change)
	}
	want := PropertyConflict{Property: "mountpoint", Want: "/srv/somewhere-else", Have: "/usr/local/jails/containers/ottermq"}
	if *change.Conflict != want {
		t.Errorf("Conflict = %+v, want %+v", *change.Conflict, want)
	}
	// And nothing ran: a conflict changes nothing, including the refquota that
	// was alongside it.
	if len(system.ran) != 0 {
		t.Errorf("ran %q on a conflict, want nothing", system.ran)
	}
}

func TestEnsureDatasetRefusesPropertiesOutsideTheAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		dataset  string
		property string
		value    string
	}{
		// Every one of these is editable according to zfs, and every one is a
		// bad idea on a jail's dataset.
		{name: "sharenfs would export the jail's data", dataset: "tank/jails/containers/ottermq/data", property: "sharenfs", value: "on"},
		{name: "canmount would stop it mounting", dataset: "tank/jails/containers/ottermq/data", property: "canmount", value: "off"},
		{name: "exec changes mount semantics", dataset: "tank/jails/containers/ottermq/data", property: "exec", value: "off"},
		{name: "jailed is ZFS delegation", dataset: "tank/jails/containers/ottermq/data", property: "jailed", value: "on"},
		{name: "quota is not refquota (JD-007)", dataset: "tank/jails/containers/ottermq/data", property: "quota", value: "10G"},
		{name: "readonly could break a running jail", dataset: "tank/jails/containers/ottermq", property: "readonly", value: "on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withJail()
			_, err := NewService(system, &fakeLogger{}, "tank").
				EnsureDataset(context.Background(), tt.dataset, map[string]string{tt.property: tt.value})
			if err == nil {
				t.Fatalf("EnsureDataset(%s=%s) returned no error", tt.property, tt.value)
			}
			if code := appErrCode(t, err); code != http.StatusBadRequest {
				t.Errorf("error code = %d, want 400", code)
			}
			if len(system.ran) != 0 {
				t.Errorf("ran %q for a refused property, want nothing", system.ran)
			}
		})
	}
}

func TestSetUserProperties(t *testing.T) {
	system := withJail()
	logger := &fakeLogger{}
	service := NewService(system, logger, "tank")

	tier := "broker"
	var removed *string // nil: remove it
	found, err := service.SetUserProperties(context.Background(), "tank/jails/containers/ottermq", map[string]*string{
		"otter:tier":     &tier,
		"otter:instance": removed,
	})
	if err != nil {
		t.Fatalf("SetUserProperties() error = %v", err)
	}

	// Read back from the system rather than assumed, so what comes out is what
	// the host now holds.
	if !reflect.DeepEqual(found, map[string]string{"otter:tier": "broker"}) {
		t.Errorf("properties = %v, want only otter:tier", found)
	}
	// atime is local on this dataset and is not a user property, so it is
	// neither returned nor touched.
	if _, leaked := found["atime"]; leaked {
		t.Errorf("properties = %v, want no native property", found)
	}

	want := []string{
		"zfs set otter:tier=broker tank/jails/containers/ottermq",
		"zfs inherit otter:instance tank/jails/containers/ottermq",
	}
	if !reflect.DeepEqual(system.ran, want) {
		t.Errorf("ran\n  %q\nwant\n  %q", system.ran, want)
	}
	// One audit entry per command, as host's ensure does: two commands, two
	// lines, each findable by ?targets=.
	if len(logger.entries) != 2 {
		t.Fatalf("wrote %d audit entries, want 2: %+v", len(logger.entries), logger.entries)
	}
	if logger.entries[0].Operation != OperationSetProperties || logger.entries[1].Operation != OperationInheritProperty {
		t.Errorf("operations = %q, %q; want %q then %q",
			logger.entries[0].Operation, logger.entries[1].Operation, OperationSetProperties, OperationInheritProperty)
	}
}

func TestSetUserPropertiesRefusesNativeNames(t *testing.T) {
	tests := []struct {
		name     string
		property string
	}{
		{name: "a native property has no colon", property: "compression"},
		{name: "so does one outside the allowlist", property: "sharenfs"},
		{name: "empty", property: ""},
		{name: "an equals sign would split the argument", property: "otter:a=b"},
		{name: "a space", property: "otter:my tag"},
		{name: "a newline", property: "otter:a\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := withJail()
			value := "x"
			_, err := NewService(system, &fakeLogger{}, "tank").SetUserProperties(
				context.Background(), "tank/jails/containers/ottermq", map[string]*string{tt.property: &value})
			if err == nil {
				t.Fatalf("SetUserProperties(%q) returned no error", tt.property)
			}
			if code := appErrCode(t, err); code != http.StatusBadRequest {
				t.Errorf("error code = %d, want 400", code)
			}
			// Refused before anything ran: one bad name in a batch must not
			// leave half of it applied.
			if len(system.ran) != 0 {
				t.Errorf("ran %q for a refused name, want nothing", system.ran)
			}
		})
	}
}

// A value is never inspected. Whatever a client puts in its own tag is the
// client's business (JD-011).
func TestSetUserPropertiesPassesAnyValueThrough(t *testing.T) {
	for _, value := range []string{"", "role=db,env=prod", "a b c", `{"json":true}`, "../../etc/passwd"} {
		system := withJail()
		v := value
		found, err := NewService(system, &fakeLogger{}, "tank").SetUserProperties(
			context.Background(), "tank/jails/containers/ottermq", map[string]*string{"otter:note": &v})
		if err != nil {
			t.Fatalf("SetUserProperties(%q) error = %v", value, err)
		}
		if found["otter:note"] != value {
			t.Errorf("otter:note = %q, want %q", found["otter:note"], value)
		}
	}
}

func TestWriteFailuresAreAuditedAndReported(t *testing.T) {
	boom := errors.New("cannot create 'tank/x': permission denied")
	system := withJail()
	system.writeErr = boom
	logger := &fakeLogger{}

	_, err := NewService(system, logger, "tank").
		EnsureDataset(context.Background(), "tank/jails/containers/ottermq/data", map[string]string{"refquota": "10G"})
	if err == nil {
		t.Fatal("EnsureDataset() returned no error")
	}
	if code := appErrCode(t, err); code != http.StatusInternalServerError {
		t.Errorf("error code = %d, want 500", code)
	}
	if len(logger.entries) != 1 {
		t.Fatalf("wrote %d audit entries, want 1 for the attempt", len(logger.entries))
	}
	if entry := logger.entries[0]; entry.Success || entry.Error == "" || entry.Command == "" {
		t.Errorf("entry = %+v, want a failure carrying the command and the reason", entry)
	}
}

func TestCheckChildName(t *testing.T) {
	tests := []struct {
		child string
		ok    bool
	}{
		{child: "data", ok: true},
		{child: "pg-data", ok: true},
		{child: "pg_data", ok: true},
		{child: "pg.data", ok: true},
		{child: "v1:data", ok: true},
		{child: "0data", ok: true},
		{child: ""},
		{child: "."},
		{child: ".."},
		{child: "-data"},  // ZFS requires an alphanumeric first
		{child: "_data"},  // same
		{child: "a/b"},    // nesting is two calls
		{child: "a b"},    //
		{child: "a@snap"}, // that is a snapshot, not a dataset
		{child: "a\nb"},   //
		{child: "../other"},
	}
	for _, tt := range tests {
		t.Run(tt.child, func(t *testing.T) {
			err := checkChildName(tt.child)
			if tt.ok && err != nil {
				t.Errorf("checkChildName(%q) = %v, want nil", tt.child, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("checkChildName(%q) = nil, want an error", tt.child)
			}
		})
	}
}

// The two allowlists differ by exactly one name. Asserted rather than assumed,
// because EnsureDataset's second check is unreachable while that holds and
// would quietly stay unreachable if someone widened only one of the lists.
func TestAllowlistsDifferOnlyByMountpoint(t *testing.T) {
	missing := make([]string, 0)
	for _, property := range createProperties {
		if !slices.Contains(updateProperties, property) {
			missing = append(missing, property)
		}
	}
	if !reflect.DeepEqual(missing, []string{"mountpoint"}) {
		t.Errorf("create-only properties = %v, want exactly [mountpoint]", missing)
	}
	for _, property := range updateProperties {
		if !slices.Contains(createProperties, property) {
			t.Errorf("%q can be updated but not set at creation, which makes it unreachable", property)
		}
	}
}

// A nil map marshals to null, which this project has shipped before. Every
// path out of EnsureDataset has to carry an object.
func TestEnsureDatasetNeverReturnsANilAppliedMap(t *testing.T) {
	tests := []struct {
		name       string
		dataset    string
		properties map[string]string
	}{
		{name: "created with no properties at all", dataset: "tank/jails/containers/ottermq/data"},
		{name: "created with an empty map", dataset: "tank/jails/containers/ottermq/data", properties: map[string]string{}},
		{name: "already there, nothing to apply", dataset: "tank/jails/containers/ottermq"},
		{name: "a conflict", dataset: "tank/jails/containers/ottermq", properties: map[string]string{"mountpoint": "/elsewhere"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change, err := NewService(withJail(), &fakeLogger{}, "tank").
				EnsureDataset(context.Background(), tt.dataset, tt.properties)
			if err != nil {
				t.Fatalf("EnsureDataset() error = %v", err)
			}
			if change.Applied == nil {
				t.Error("Applied is nil, which marshals to null")
			}
		})
	}
}

// zfs ran and refused: the request is wrong, not the server. A client told 500
// would retry a value that can never work.
func TestZFSRefusingAValueIsAClientError(t *testing.T) {
	system := withJail()
	system.writeErr = commandError("exit status 255",
		"cannot set property for 'tank/x': 'recordsize' must be power of 2 from 512B to 16M", 255)

	_, err := NewService(system, &fakeLogger{}, "tank").
		EnsureDataset(context.Background(), "tank/jails/containers/ottermq/data", map[string]string{"recordsize": "7K"})
	if err == nil {
		t.Fatal("EnsureDataset() returned no error")
	}
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Errorf("error code = %d, want 400 for a value zfs refused", code)
	}
	if !strings.Contains(err.Error(), "power of 2") {
		t.Errorf("error = %q, want zfs's own sentence in it", err)
	}
}

// But a command that never ran, or timed out, is the server's problem: both
// report exit code -1, and that is the only distinction available here.
func TestACommandThatNeverRanIsAServerError(t *testing.T) {
	system := withJail()
	system.writeErr = commandError("fork/exec /sbin/zfs: no such file or directory", "", -1)

	_, err := NewService(system, &fakeLogger{}, "tank").
		EnsureDataset(context.Background(), "tank/jails/containers/ottermq/data", map[string]string{"refquota": "1G"})
	if code := appErrCode(t, err); code != http.StatusInternalServerError {
		t.Errorf("error code = %d, want 500 for a command that never ran", code)
	}
}

// commandError builds what the real runner returns for a command that ran and
// failed: exitCode above zero, or -1 for one that never started.
func commandError(message, stderr string, exitCode int) error {
	return &system.CommandError{
		Command: "zfs",
		Result:  system.CommandResult{Stderr: stderr, ExitCode: exitCode},
		Err:     errors.New(message + ": " + stderr),
	}
}
