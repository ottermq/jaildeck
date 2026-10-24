package jails

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func withChildren() Spec {
	spec := validSpec()
	spec.Datasets = []ChildDataset{
		{Name: "data", Properties: map[string]string{"refquota": "2G"}},
		{Name: "logs", Properties: map[string]string{"refquota": "512M", "compression": "zstd"}},
	}
	spec.Properties = map[string]string{"otter:instance": "i-42"}
	return spec
}

// --- what a spec may ask for -------------------------------------------

func TestChildDatasetRefusals(t *testing.T) {
	tests := []struct {
		name     string
		datasets []ChildDataset
		message  string
	}{
		{
			name:     "no name",
			datasets: []ChildDataset{{Properties: map[string]string{"refquota": "1G"}}},
			message:  "datasets[0] has no name",
		},
		{
			// The child endpoints take one level at a time, and a slash here
			// would name a dataset two levels down.
			name:     "a name two levels down",
			datasets: []ChildDataset{{Name: "data/inner"}},
			message:  "may not contain a slash",
		},
		{
			name:     "the same dataset twice",
			datasets: []ChildDataset{{Name: "data"}, {Name: "data"}},
			message:  `datasets names "data" twice`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			spec.Datasets = test.datasets

			_, err := spec.normalize()
			if err == nil {
				t.Fatalf("want a refusal for %+v", test.datasets)
			}
			if code := appCode(t, err); code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Errorf("error = %q, want it to mention %q", err, test.message)
			}
		})
	}
}

// Measured on 15.1-RELEASE-p5: ZFS creates such a dataset without complaint,
// the jail cannot see it, and `zfs destroy -r` on the jail destroys it
// anyway -- so the jail's destroy would take a dataset living somewhere else
// on the host.
func TestChildMountpointHasToBeInsideTheJail(t *testing.T) {
	const jailPath = "/usr/local/jails/containers/db1"
	outside := []string{
		"/srv/elsewhere",
		"/usr/local/jails/containers/db1x/data", // the separator is what stops this
		"none",
		"legacy",
		"relative/path",
		"/usr/local/jails/containers/db1/../db2/data",
	}
	for _, mountpoint := range outside {
		err := checkChildMountpoint(jailPath, "data", map[string]string{"mountpoint": mountpoint})
		if err == nil {
			t.Errorf("mountpoint %q was accepted", mountpoint)
			continue
		}
		if code := appCode(t, err); code != http.StatusBadRequest {
			t.Errorf("mountpoint %q: status = %d, want %d", mountpoint, code, http.StatusBadRequest)
		}
	}

	inside := []string{
		jailPath,
		jailPath + "/data",
		jailPath + "/var/db/postgres",
	}
	for _, mountpoint := range inside {
		if err := checkChildMountpoint(jailPath, "data", map[string]string{"mountpoint": mountpoint}); err != nil {
			t.Errorf("mountpoint %q was refused: %v", mountpoint, err)
		}
	}

	// Not asked for at all is inherited, which lands under the jail by
	// construction.
	if err := checkChildMountpoint(jailPath, "data", map[string]string{"refquota": "1G"}); err != nil {
		t.Errorf("an inherited mountpoint was refused: %v", err)
	}
}

// --- creating them -----------------------------------------------------

func TestCreateMakesTheChildDatasetsAndTags(t *testing.T) {
	host := newEnsureHost()

	result, err := host.service().Create(context.Background(), withChildren())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(result.Datasets) != 2 {
		t.Fatalf("reported %d datasets, want 2: %+v", len(result.Datasets), result.Datasets)
	}
	for _, change := range result.Datasets {
		if !change.Created {
			t.Errorf("%s was not reported as created", change.Dataset)
		}
	}
	if got := host.storage.children["data"].Properties["refquota"]; got != "2G" {
		t.Errorf("data's refquota = %q, want 2G", got)
	}
	if got := host.storage.children["logs"].Properties["compression"]; got != "zstd" {
		t.Errorf("logs's compression = %q, want zstd", got)
	}
	if got := result.Properties["otter:instance"]; got != "i-42" {
		t.Errorf("properties = %v, want the tag the spec asked for", result.Properties)
	}
}

// Before the stanza is written and long before the jail starts: a jail whose
// /etc/rc expects its data mounted should find it there, and a quota that
// arrives after the first write was not in force when it mattered.
func TestChildDatasetsAreMadeBeforeTheJailStarts(t *testing.T) {
	host := newEnsureHost()
	host.config.err = errors.New("the disk is full")

	_, err := host.service().Create(context.Background(), withChildren())
	if err == nil {
		t.Fatal("want the configure step's error")
	}
	if len(host.storage.ensured) != 2 {
		t.Errorf("made %v, want both datasets before the stanza was written", host.storage.ensured)
	}
	if len(host.system.started) != 0 {
		t.Errorf("started the jail: %v", host.system.started)
	}
}

func TestCreateStopsWhenAChildDatasetCannotBeMade(t *testing.T) {
	host := newEnsureHost()
	host.storage.ensureErr = errors.New("cannot create: out of space")

	result, err := host.service().Create(context.Background(), withChildren())
	if err == nil {
		t.Fatal("want the error")
	}
	if result.FailedAt != StepDatasets {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepDatasets)
	}
	// Nothing was undone, and the root it cloned is named so an operator can
	// find it (JD-030).
	if result.Root == "" {
		t.Error("the body does not say what exists")
	}
	if len(host.config.written) != 0 {
		t.Errorf("wrote a stanza for a jail whose datasets failed: %v", host.config.written)
	}
}

// mountpoint is settable only at creation (JD-023), so a child already
// somewhere else is a jail whose data is not where the spec says it is.
func TestCreateStopsWhenAChildIsSomewhereElse(t *testing.T) {
	host := newEnsureHost()
	host.storage.conflict = &PropertyConflict{
		Property: "mountpoint",
		Want:     "/usr/local/jails/containers/db1/data",
		Have:     "/srv/old",
	}

	result, err := host.service().Create(context.Background(), withChildren())
	if err == nil {
		t.Fatal("want the conflict")
	}
	if code := appCode(t, err); code != http.StatusConflict {
		t.Errorf("status = %d, want %d", code, http.StatusConflict)
	}
	if !strings.Contains(err.Error(), "/srv/old") {
		t.Errorf("error = %q, want it to name where the dataset actually is", err)
	}
	if result.FailedAt != StepDatasets {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepDatasets)
	}
}

func TestCreateRefusesAChildMountedOutsideTheJail(t *testing.T) {
	host := newEnsureHost()
	spec := validSpec()
	spec.Datasets = []ChildDataset{{Name: "data", Properties: map[string]string{"mountpoint": "/srv/elsewhere"}}}

	result, err := host.service().Create(context.Background(), spec)
	if err == nil {
		t.Fatal("want the refusal")
	}
	if code := appCode(t, err); code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
	if result.FailedAt != StepDatasets {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepDatasets)
	}
	if len(host.storage.ensured) != 0 {
		t.Errorf("created %v after refusing the spec", host.storage.ensured)
	}
}

// Without the port there is nothing to create them with, and a jail that
// quietly came up without its data would be worse than an error.
func TestCreateRefusesDatasetsItCannotMake(t *testing.T) {
	system := &createSystem{}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf", system: system}
	service := createService(system, &stubCloner{root: clonedRoot()}, config, &stubAutostart{}, &fakeLogger{})

	for _, spec := range []Spec{withChildren(), func() Spec {
		s := validSpec()
		s.Properties = map[string]string{"otter:instance": "i-42"}
		return s
	}()} {
		_, err := service.Create(context.Background(), spec)
		if err == nil {
			t.Fatal("want a refusal with no dataset writer wired")
		}
		if code := appCode(t, err); code != http.StatusNotImplemented {
			t.Errorf("status = %d, want %d", code, http.StatusNotImplemented)
		}
	}
}

// --- comparing them ----------------------------------------------------

func TestEnsureWithChildDatasetsIsANoOpWhenRepeated(t *testing.T) {
	host := newEnsureHost()
	service := host.service()

	if _, err := service.Ensure(context.Background(), "db1", withChildren()); err != nil {
		t.Fatalf("the first Ensure: %v", err)
	}
	second, err := service.Ensure(context.Background(), "db1", withChildren())
	if err != nil {
		t.Fatalf("the second Ensure: %v", err)
	}
	if second.Changed {
		t.Error("the second Ensure changed something")
	}
	if len(second.Differences) != 0 {
		t.Errorf("the same spec differs from the jail it made: %+v", second.Differences)
	}
}

func TestEnsureReportsStorageDifferences(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		want   string
		have   string
		change func(*Spec)
		host   func(*ensureHost)
	}{
		{
			name: "a child the jail does not have", field: "datasets.cache",
			want: "tank/jails/containers/db1/cache",
			change: func(s *Spec) {
				s.Datasets = append(s.Datasets, ChildDataset{Name: "cache"})
			},
		},
		{
			name: "a child nothing asked for", field: "datasets.stray",
			have: "tank/jails/containers/db1/stray",
			host: func(h *ensureHost) {
				h.storage.children["stray"] = Dataset{
					Name:       "tank/jails/containers/db1/stray",
					Mountpoint: "/usr/local/jails/containers/db1/stray",
				}
			},
		},
		{
			name: "a quota that was grown", field: "datasets.data.refquota",
			want: "4G", have: "2G",
			change: func(s *Spec) { s.Datasets[0].Properties["refquota"] = "4G" },
		},
		{
			name: "a mountpoint the dataset does not have", field: "datasets.data.mountpoint",
			want: "/usr/local/jails/containers/db1/var/db/postgres",
			have: "/usr/local/jails/containers/db1/data",
			change: func(s *Spec) {
				s.Datasets[0].Properties["mountpoint"] = "/usr/local/jails/containers/db1/var/db/postgres"
			},
		},
		{
			name: "a tag with another value", field: "properties.otter:instance",
			want: "i-99", have: "i-42",
			change: func(s *Spec) { s.Properties["otter:instance"] = "i-99" },
		},
		{
			name: "a tag the jail does not carry", field: "properties.otter:tier",
			want:   "gold",
			change: func(s *Spec) { s.Properties["otter:tier"] = "gold" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newEnsureHost()
			service := host.service()
			if _, err := service.Ensure(context.Background(), "db1", withChildren()); err != nil {
				t.Fatalf("the create: %v", err)
			}

			spec := withChildren()
			if test.change != nil {
				test.change(&spec)
			}
			if test.host != nil {
				test.host(host)
			}

			result, err := service.Ensure(context.Background(), "db1", spec)
			if err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			found := false
			for _, difference := range result.Differences {
				if difference.Field != test.field {
					continue
				}
				found = true
				if difference.Want != test.want || difference.Have != test.have {
					t.Errorf("%s = want %q have %q, wanted want %q have %q",
						difference.Field, difference.Want, difference.Have, test.want, test.have)
				}
			}
			if !found {
				t.Errorf("no difference named %q; got %+v", test.field, result.Differences)
			}
			// Left alone: nothing is reconciled (JD-016).
			if result.Changed {
				t.Error("changed = true")
			}
		})
	}
}

// A spec that could never be built gets the same answer whichever door it
// comes through: the create refuses it at the datasets step, and so does a
// comparison against a jail that already exists.
func TestEnsureRefusesAChildMountedOutsideTheJail(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", withChildren()); err != nil {
		t.Fatalf("the create: %v", err)
	}

	spec := withChildren()
	spec.Datasets[0].Properties["mountpoint"] = "/srv/elsewhere"

	result, err := service.Ensure(context.Background(), "db1", spec)
	if err == nil {
		t.Fatal("want the refusal, not a difference a client could never close")
	}
	if code := appCode(t, err); code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
	if len(result.Differences) != 0 {
		t.Errorf("reported differences as well: %+v", result.Differences)
	}
}

// A tag another tool set is not this spec's business: the write side merges
// rather than replaces, for exactly that reason (JD-004, JD-023). The same
// for a native property the spec does not mention.
func TestEnsureIgnoresWhatTheSpecDoesNotClaim(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", withChildren()); err != nil {
		t.Fatalf("the create: %v", err)
	}
	host.storage.properties["someone-else:owner"] = "them"
	data := host.storage.children["data"]
	data.Properties["recordsize"] = "16K"
	host.storage.children["data"] = data

	result, err := service.Ensure(context.Background(), "db1", withChildren())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(result.Differences) != 0 {
		t.Errorf("something nobody claimed was reported: %+v", result.Differences)
	}
}

// The spec describes one level, so a dataset deeper than that is one it can
// neither ask for nor disown -- and a refusal nobody can resolve is worse
// than a comparison that says what it covers.
func TestEnsureIgnoresDatasetsDeeperThanTheSpecReaches(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", withChildren()); err != nil {
		t.Fatalf("the create: %v", err)
	}
	host.storage.children["data/inner"] = Dataset{
		Name:       "tank/jails/containers/db1/data/inner",
		Mountpoint: "/usr/local/jails/containers/db1/data/inner",
	}

	result, err := service.Ensure(context.Background(), "db1", withChildren())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(result.Differences) != 0 {
		t.Errorf("a grandchild was reported: %+v", result.Differences)
	}
}

// The listing degrades to no datasets when the pool cannot be read (JD-004).
// A comparison that did the same would report every child as missing.
func TestEnsureFailsRatherThanComparingAgainstAnUnreadablePool(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", withChildren()); err != nil {
		t.Fatalf("the create: %v", err)
	}
	host.storage.readErr = errors.New("cannot open 'tank': pool I/O is currently suspended")

	_, err := service.Ensure(context.Background(), "db1", withChildren())
	if err == nil {
		t.Fatal("want the error rather than a diff built on nothing")
	}
	if code := appCode(t, err); code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", code, http.StatusInternalServerError)
	}
}
