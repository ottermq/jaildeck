package jails

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// --- doubles -----------------------------------------------------------

// stubCloner records what it was asked and answers what the test set up.
type stubCloner struct {
	checkErr error
	cloneErr error
	root     Root

	checked string
	cloned  []string
}

func (s *stubCloner) CheckTemplate(_ context.Context, snapshot string) error {
	s.checked = snapshot
	return s.checkErr
}

func (s *stubCloner) CloneRoot(_ context.Context, snapshot, name string) (Root, error) {
	s.cloned = append(s.cloned, snapshot+" -> "+name)
	if s.cloneErr != nil {
		return Root{}, s.cloneErr
	}
	return s.root, nil
}

// stubConfig stands in for the file in jail.conf.d, and -- when it is given a
// system -- for what writing that file does: the jail appears, stopped, in
// the next listing. Jail Deck discovers a configured jail from its
// configuration (JD-004), so a double where the write changes nothing would
// let a broken read path pass.
type stubConfig struct {
	err     error
	path    string
	system  *createSystem
	written map[string]string
}

func (s *stubConfig) WriteConfig(_ context.Context, name, content string) (string, error) {
	if s.written == nil {
		s.written = map[string]string{}
	}
	s.written[name] = content
	if s.err != nil {
		return "", s.err
	}
	if s.system != nil {
		s.system.put(Jail{
			Name: name, Status: JailStatusStopped,
			Path: "/usr/local/jails/containers/" + name,
		})
	}
	return s.path, nil
}

type stubAutostart struct {
	err  error
	set  map[string]bool
	call int
	// steps, when set, is shared with the other doubles so a test can assert
	// the order the steps ran in and not merely that each one did.
	steps *[]string
}

func (s *stubAutostart) SetAutostart(_ context.Context, name string, enabled bool) (string, error) {
	if s.set == nil {
		s.set = map[string]bool{}
	}
	s.call++
	record(s.steps, "autostart")
	s.set[name] = enabled
	return "sysrc jail_list+=" + name, s.err
}

// listSystemWith answers List with the jails given, and starts whatever it is
// asked to start -- the adapter's job of confirming the start is its own, and
// tested where it lives.
type createSystem struct {
	existing []Jail
	listErr  error
	startErr error
	stopErr  error
	started  []string
	stopped  []string
	steps    *[]string
	// onStart, when set, runs as the jail comes up -- the hook for what the
	// stanza's own exec.poststart does to the host, which happens whether
	// the start then succeeds or not.
	onStart func(name string)
}

// record appends a step to a shared list, for the tests that care which
// order the doubles were called in. A nil list means nobody is watching.
func record(steps *[]string, step string) {
	if steps != nil {
		*steps = append(*steps, step)
	}
}

func (s *createSystem) List(context.Context) ([]Jail, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.existing, nil
}

// put adds or replaces a jail, the way the host would.
func (s *createSystem) put(jail Jail) {
	for i, existing := range s.existing {
		if existing.Name == jail.Name {
			s.existing[i] = jail
			return
		}
	}
	s.existing = append(s.existing, jail)
}

func (s *createSystem) Start(_ context.Context, name string) (OperationResult, error) {
	s.started = append(s.started, name)
	if s.onStart != nil {
		s.onStart(name)
	}
	if s.startErr != nil {
		return OperationResult{Jail: Jail{Name: name}, Command: "service jail start " + name}, s.startErr
	}
	jail := Jail{Name: name, Status: JailStatusRunning, JID: "42",
		Path: "/usr/local/jails/containers/" + name}
	s.put(jail)
	return OperationResult{Jail: jail, Command: "service jail start " + name}, nil
}

// Stop takes the jail down the way the host would, so a destroy that runs
// afterwards sees a stopped jail rather than the one it asked to stop.
func (s *createSystem) Stop(_ context.Context, name string) (OperationResult, error) {
	s.stopped = append(s.stopped, name)
	record(s.steps, "stop")
	if s.stopErr != nil {
		return OperationResult{Jail: Jail{Name: name}, Command: "service jail stop " + name}, s.stopErr
	}
	for i, existing := range s.existing {
		if existing.Name == name {
			s.existing[i].Status = JailStatusStopped
			s.existing[i].JID = ""
			return OperationResult{Jail: s.existing[i], Command: "service jail stop " + name}, nil
		}
	}
	return OperationResult{Jail: Jail{Name: name}, Command: "service jail stop " + name}, nil
}
func (s *createSystem) Restart(context.Context, string) (OperationResult, error) {
	return OperationResult{}, nil
}

var (
	_ JailSystem   = (*createSystem)(nil)
	_ RootCloner   = (*stubCloner)(nil)
	_ ConfigWriter = (*stubConfig)(nil)
	_ Autostarter  = (*stubAutostart)(nil)
)

func createService(system *createSystem, cloner *stubCloner, config *stubConfig, autostart *stubAutostart, logger *fakeLogger) *JailService {
	return NewJailService(Dependencies{
		System: system, Audit: logger,
		RootCloner: cloner, Config: config, Autostart: autostart,
	})
}

func validSpec() Spec {
	return Spec{
		Name:     "db1",
		Template: "tank/jails/templates/15.1-RELEASE@base",
		Network:  Network{Mode: NetworkAlias, IP4: "192.0.2.10/24", Interface: "em0"},
	}
}

func clonedRoot() Root {
	return Root{Dataset: "tank/jails/containers/db1", Mountpoint: "/usr/local/jails/containers/db1"}
}

// --- the spec ----------------------------------------------------------

func TestSpecNormalizeRefuses(t *testing.T) {
	tests := []struct {
		name    string
		spec    Spec
		code    int
		message string
	}{
		{
			name:    "no name",
			spec:    Spec{Template: "p/t@base"},
			code:    http.StatusBadRequest,
			message: "name is required",
		},
		{
			// jail(8) reads a dotted name as a hierarchical jail and rc.d/jail
			// maps the dot to an underscore, so two such names collide there.
			name:    "a dot in the name",
			spec:    Spec{Name: "db.1", Template: "p/t@base"},
			code:    http.StatusBadRequest,
			message: "cannot be created",
		},
		{
			name:    "leading dash",
			spec:    Spec{Name: "-db", Template: "p/t@base"},
			code:    http.StatusBadRequest,
			message: "has to begin with a letter or a digit",
		},
		{
			name:    "name too long",
			spec:    Spec{Name: strings.Repeat("a", 64), Template: "p/t@base"},
			code:    http.StatusBadRequest,
			message: "longer than 63 characters",
		},
		{
			name:    "no template",
			spec:    Spec{Name: "db1"},
			code:    http.StatusBadRequest,
			message: "template is required",
		},
		{
			name:    "template is not a snapshot",
			spec:    Spec{Name: "db1", Template: "tank/jails/templates/15.1-RELEASE"},
			code:    http.StatusBadRequest,
			message: "has to be dataset@snapshot",
		},
		{
			name:    "alias without an address",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkAlias, Interface: "em0"}},
			code:    http.StatusBadRequest,
			message: "network.ip4 is required",
		},
		{
			name:    "alias without an interface",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkAlias, IP4: "192.0.2.10/24"}},
			code:    http.StatusBadRequest,
			message: "network.interface is required",
		},
		{
			// Dropping it silently is how a jail ends up reachable when
			// whoever wrote the spec believed it was not.
			name:    "an address for a mode that does not read one",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkInherit, IP4: "192.0.2.10/24"}},
			code:    http.StatusBadRequest,
			message: `network.ip4 is only read for modes "alias" and "vnet"`,
		},
		{
			name:    "an interface for vnet, which reaches the network by a bridge",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkVNET, Bridge: "bridge0", Interface: "em0"}},
			code:    http.StatusBadRequest,
			message: `network.interface is only read for mode "alias"`,
		},
		{
			name:    "a bridge for a mode with no epair",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkInherit, Bridge: "bridge0"}},
			code:    http.StatusBadRequest,
			message: `network.bridge is only read for mode "vnet"`,
		},
		{
			name:    "private for a mode with no bridge membership",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkDisable, Private: true}},
			code:    http.StatusBadRequest,
			message: `network.private is only read for mode "vnet"`,
		},
		{
			name:    "a gateway for a mode that sets no route",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkAlias, IP4: "192.0.2.10/24", Interface: "em0", Gateway: "192.0.2.1"}},
			code:    http.StatusBadRequest,
			message: `network.gateway is only read for mode "vnet"`,
		},
		{
			name:    "an unknown mode",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: "bridged"}},
			code:    http.StatusBadRequest,
			message: `unknown network mode "bridged"`,
		},
		{
			// Decided in JD-017 and not built. "Unknown mode" would be a lie.
			// The bridge is the operator's and Jail Deck never creates one
			// (JD-017), so there is no sensible default to fall back on.
			name:    "vnet without a bridge",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkVNET}},
			code:    http.StatusBadRequest,
			message: "network.bridge is required",
		},
		{
			name:    "a gateway with no address to leave by",
			spec:    Spec{Name: "db1", Template: "p/t@base", Network: Network{Mode: NetworkVNET, Bridge: "bridge0", Gateway: "10.0.0.1"}},
			code:    http.StatusBadRequest,
			message: "network.gateway needs network.ip4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.spec.normalize()
			if err == nil {
				t.Fatalf("normalize(%+v) = nil error, want one", tt.spec)
			}
			var appErr *common.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("error is %T, want *common.AppError", err)
			}
			if appErr.Code != tt.code {
				t.Errorf("code = %d, want %d (%s)", appErr.Code, tt.code, appErr.Error())
			}
			if !strings.Contains(appErr.Error(), tt.message) {
				t.Errorf("message = %q, want it to contain %q", appErr.Error(), tt.message)
			}
		})
	}
}

func TestSpecNormalizeDefaults(t *testing.T) {
	spec, err := Spec{Name: "db1", Template: "p/t@base"}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if spec.Hostname != "db1" {
		t.Errorf("hostname = %q, want the jail's own name", spec.Hostname)
	}
	// The jail that is hardest to get wrong, not the one that is handiest.
	if spec.Network.Mode != NetworkDisable {
		t.Errorf("network mode = %q, want %q", spec.Network.Mode, NetworkDisable)
	}
	if !spec.starts() {
		t.Error("starts() = false, want true: a spec that says nothing starts the jail")
	}
	no := false
	if (Spec{Start: &no}).starts() {
		t.Error("starts() = true for start:false")
	}
}

// --- the stanza --------------------------------------------------------

func TestStanza(t *testing.T) {
	tests := []struct {
		name    string
		network Network
		want    []string
		absent  []string
	}{
		{
			name:    "alias",
			network: Network{Mode: NetworkAlias, IP4: "192.0.2.10/24", Interface: "em0"},
			want: []string{
				"ip4.addr = 192.0.2.10/24;",
				"interface = em0;",
				"ip6 = disable;",
			},
			absent: []string{"ip4 = alias"},
		},
		{
			name:    "inherit",
			network: Network{Mode: NetworkInherit},
			want:    []string{"ip4 = inherit;", "ip6 = inherit;"},
			absent:  []string{"ip4.addr", "interface ="},
		},
		{
			name:    "disable",
			network: Network{Mode: NetworkDisable},
			want:    []string{"ip4 = disable;", "ip6 = disable;"},
			absent:  []string{"ip4.addr"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := Spec{Name: "db1", Hostname: "db1.example", Network: tt.network}
			got, err := spec.Stanza(Placement{Path: "/usr/local/jails/containers/db1", Epair: "epair7"})
			if err != nil {
				t.Fatalf("Stanza: %v", err)
			}
			for _, want := range append(tt.want,
				"db1 {",
				`host.hostname = "db1.example";`,
				`path = "/usr/local/jails/containers/db1";`,
				`exec.start = "/bin/sh /etc/rc";`,
				"mount.devfs;",
			) {
				if !strings.Contains(got, want) {
					t.Errorf("stanza is missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("stanza should not contain %q:\n%s", absent, got)
				}
			}
		})
	}
}

// The adapter's own parser only strips '#'. A stanza commented with '//' would
// be written and then never read back, so the jail would vanish from the
// listing.
func TestStanzaCommentsAreHash(t *testing.T) {
	got, err := Spec{Name: "db1", Hostname: "db1", Network: Network{Mode: NetworkDisable}}.Stanza(Placement{Path: "/p"})
	if err != nil {
		t.Fatalf("Stanza: %v", err)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			t.Errorf("stanza has a // comment, which Jail Deck's own parser does not strip: %q", line)
		}
	}
	if !strings.Contains(got, "# Written by Jail Deck") {
		t.Errorf("stanza carries no provenance comment:\n%s", got)
	}
}

// A clone that reported no mountpoint would produce a jail with no path, and
// jail(8) would refuse the stanza. Caught before the file is written.
func TestStanzaRefusesAnEmptyPath(t *testing.T) {
	_, err := Spec{Name: "db1", Network: Network{Mode: NetworkDisable}}.Stanza(Placement{})
	if err == nil {
		t.Fatal("Stanza with no path = nil error, want one")
	}
	if !strings.Contains(err.Error(), "no mountpoint") {
		t.Errorf("message = %q, want it to mention the missing mountpoint", err)
	}
}

// --- creating ----------------------------------------------------------

func TestCreateHappyPath(t *testing.T) {
	system := &createSystem{}
	cloner := &stubCloner{root: clonedRoot()}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf"}
	autostart := &stubAutostart{}
	logger := &fakeLogger{}

	spec := validSpec()
	spec.Autostart = true

	got, err := createService(system, cloner, config, autostart, logger).
		Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got.FailedAt != "" {
		t.Errorf("failed_at = %q, want empty", got.FailedAt)
	}
	if got.Root != "tank/jails/containers/db1" || got.Mountpoint != "/usr/local/jails/containers/db1" {
		t.Errorf("root = %q at %q, want the cloned dataset", got.Root, got.Mountpoint)
	}
	if got.From != spec.Template {
		t.Errorf("from = %q, want %q", got.From, spec.Template)
	}
	if got.Config != "/etc/jail.conf.d/db1.conf" {
		t.Errorf("config = %q, want the file the stanza was written to", got.Config)
	}
	if !got.Autostart {
		t.Error("autostart = false, want true")
	}
	if !got.Running || got.Jail.Status != JailStatusRunning {
		t.Errorf("running = %v, jail status = %q, want a running jail", got.Running, got.Jail.Status)
	}

	// The template is checked before anything is cloned.
	if cloner.checked != spec.Template {
		t.Errorf("checked %q, want %q", cloner.checked, spec.Template)
	}
	if len(cloner.cloned) != 1 {
		t.Fatalf("cloned %d times, want once: %v", len(cloner.cloned), cloner.cloned)
	}
	// The stanza written is the one rendered for this spec.
	if !strings.Contains(config.written["db1"], "ip4.addr = 192.0.2.10/24;") {
		t.Errorf("the stanza written is not the one this spec asks for:\n%s", config.written["db1"])
	}
	if !autostart.set["db1"] {
		t.Error("the jail was not added to jail_list")
	}
	if len(system.started) != 1 || system.started[0] != "db1" {
		t.Errorf("started = %v, want [db1]", system.started)
	}

	// Both steps this domain runs itself are audited.
	seen := map[string]bool{}
	for _, entry := range logger.entries {
		seen[entry.Operation] = entry.Success
	}
	for _, operation := range []string{OperationWriteConfig, OperationSetAutostart, OperationStart.String()} {
		if success, logged := seen[operation]; !logged || !success {
			t.Errorf("audit log has no successful %q entry: %v", operation, seen)
		}
	}
}

func TestCreateWithoutStarting(t *testing.T) {
	system := &createSystem{}
	cloner := &stubCloner{root: clonedRoot()}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf", system: system}

	no := false
	spec := validSpec()
	spec.Start = &no

	got, err := createService(system, cloner, config, &stubAutostart{}, &fakeLogger{}).
		Create(context.Background(), spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(system.started) != 0 {
		t.Errorf("started = %v, want nothing started", system.started)
	}
	if got.Running {
		t.Error("running = true for a spec that asked not to start")
	}
	// The jail is read back from the host rather than described from the
	// spec, so it is the stopped jail the configuration file just created.
	if got.Jail.Name != "db1" || got.Jail.Status != JailStatusStopped {
		t.Errorf("jail = %+v, want the stopped db1 the host now reports", got.Jail)
	}
	if got.Config == "" || got.Root == "" {
		t.Errorf("config = %q and root = %q, want both reported", got.Config, got.Root)
	}
}

func TestCreateRefusesANameAlreadyTaken(t *testing.T) {
	system := &createSystem{existing: []Jail{{Name: "db1", Status: JailStatusRunning}}}
	cloner := &stubCloner{root: clonedRoot()}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf"}

	_, err := createService(system, cloner, config, &stubAutostart{}, &fakeLogger{}).
		Create(context.Background(), validSpec())
	if err == nil {
		t.Fatal("Create over an existing jail = nil error, want a conflict")
	}
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusConflict {
		t.Fatalf("error = %v, want 409", err)
	}
	// And nothing was touched on the way to refusing.
	if len(cloner.cloned) != 0 || len(config.written) != 0 {
		t.Errorf("a refused create cloned %v and wrote %v", cloner.cloned, config.written)
	}
}

func TestCreateReportsTheStepThatFailed(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(*createSystem, *stubCloner, *stubConfig, *stubAutostart)
		wantStep string
		// wantRoot says the response still reports what exists: nothing is
		// rolled back (JD-030).
		wantRoot   bool
		wantConfig bool
	}{
		{
			name: "the template cannot be cloned",
			arrange: func(_ *createSystem, c *stubCloner, _ *stubConfig, _ *stubAutostart) {
				c.checkErr = common.NewAppError(http.StatusNotFound, "no snapshot named p/t@base")
			},
			wantStep: StepCheck,
		},
		{
			name: "the clone fails",
			arrange: func(_ *createSystem, c *stubCloner, _ *stubConfig, _ *stubAutostart) {
				c.cloneErr = errors.New("out of space")
			},
			wantStep: StepClone,
		},
		{
			name: "the stanza cannot be written",
			arrange: func(_ *createSystem, _ *stubCloner, cfg *stubConfig, _ *stubAutostart) {
				cfg.err = common.NewAppError(http.StatusConflict, "already exists")
			},
			wantStep: StepConfigure,
			wantRoot: true,
		},
		{
			name: "autostart fails",
			arrange: func(_ *createSystem, _ *stubCloner, _ *stubConfig, a *stubAutostart) {
				a.err = errors.New("sysrc: unknown variable")
			},
			wantStep:   StepAutostart,
			wantRoot:   true,
			wantConfig: true,
		},
		{
			name: "the jail will not start",
			arrange: func(s *createSystem, _ *stubCloner, _ *stubConfig, _ *stubAutostart) {
				s.startErr = errors.New(`jail status is 'stopped', want 'running'`)
			},
			wantStep:   StepStart,
			wantRoot:   true,
			wantConfig: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := &createSystem{}
			cloner := &stubCloner{root: clonedRoot()}
			config := &stubConfig{path: "/etc/jail.conf.d/db1.conf"}
			autostart := &stubAutostart{}
			tt.arrange(system, cloner, config, autostart)

			spec := validSpec()
			spec.Autostart = true

			got, err := createService(system, cloner, config, autostart, &fakeLogger{}).
				Create(context.Background(), spec)
			if err == nil {
				t.Fatalf("Create = nil error, want a failure at %s", tt.wantStep)
			}
			if got.FailedAt != tt.wantStep {
				t.Errorf("failed_at = %q, want %q", got.FailedAt, tt.wantStep)
			}
			if got.Running {
				t.Error("running = true after a failure")
			}
			if (got.Root != "") != tt.wantRoot {
				t.Errorf("root = %q, wantRoot = %v: a failure reports what exists", got.Root, tt.wantRoot)
			}
			if (got.Config != "") != tt.wantConfig {
				t.Errorf("config = %q, wantConfig = %v", got.Config, tt.wantConfig)
			}
		})
	}
}

// Autostart is not touched unless it was asked for: a create must not quietly
// add a jail to jail_list.
func TestCreateLeavesAutostartAloneUnlessAsked(t *testing.T) {
	autostart := &stubAutostart{}
	got, err := createService(&createSystem{}, &stubCloner{root: clonedRoot()},
		&stubConfig{path: "/etc/jail.conf.d/db1.conf"}, autostart, &fakeLogger{}).
		Create(context.Background(), validSpec())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if autostart.call != 0 {
		t.Errorf("SetAutostart called %d times, want 0", autostart.call)
	}
	if got.Autostart {
		t.Error("autostart = true, want false")
	}
}

// A Jail Deck with no storage wired answers 501 rather than failing somewhere
// in the middle.
func TestCreateWithoutStorage(t *testing.T) {
	svc := NewJailService(Dependencies{System: &createSystem{}, Audit: &fakeLogger{}})
	_, err := svc.Create(context.Background(), validSpec())
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusNotImplemented {
		t.Fatalf("error = %v, want 501", err)
	}
}

// A spec that asks for autostart on a Jail Deck that cannot set it is refused
// before anything is created, not after the jail is already there.
func TestCreateRefusesAutostartItCannotSet(t *testing.T) {
	cloner := &stubCloner{root: clonedRoot()}
	svc := NewJailService(Dependencies{
		System: &createSystem{}, Audit: &fakeLogger{},
		RootCloner: cloner, Config: &stubConfig{path: "/p"},
	})
	spec := validSpec()
	spec.Autostart = true

	_, err := svc.Create(context.Background(), spec)
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusNotImplemented {
		t.Fatalf("error = %v, want 501", err)
	}
	if len(cloner.cloned) != 0 {
		t.Errorf("cloned %v before refusing", cloner.cloned)
	}
}

// The host could not be read, so whether the name is free is unknown. Creating
// anyway is how two things end up called the same thing.
func TestCreateRefusesWhenTheHostCannotBeRead(t *testing.T) {
	cloner := &stubCloner{root: clonedRoot()}
	_, err := createService(&createSystem{listErr: errors.New("jls: Operation not permitted")},
		cloner, &stubConfig{path: "/p"}, &stubAutostart{}, &fakeLogger{}).
		Create(context.Background(), validSpec())
	if err == nil {
		t.Fatal("Create with an unreadable host = nil error, want one")
	}
	if len(cloner.cloned) != 0 {
		t.Errorf("cloned %v without knowing whether the name was free", cloner.cloned)
	}
}

// A failure before anything exists has nothing to report but its reason, and
// a Created full of empty strings would bury it. A failure after the root
// exists reports both.
func TestCreatedCarriesTheReasonOnlyWhenItCarriesState(t *testing.T) {
	t.Run("nothing created", func(t *testing.T) {
		cloner := &stubCloner{cloneErr: errors.New("out of space")}
		got, err := createService(&createSystem{}, cloner,
			&stubConfig{path: "/p"}, &stubAutostart{}, &fakeLogger{}).
			Create(context.Background(), validSpec())
		if err == nil {
			t.Fatal("Create = nil error, want one")
		}
		if got.Root != "" {
			t.Errorf("root = %q, want empty: the clone failed", got.Root)
		}
	})

	t.Run("the root exists", func(t *testing.T) {
		config := &stubConfig{err: errors.New("permission denied")}
		got, err := createService(&createSystem{}, &stubCloner{root: clonedRoot()},
			config, &stubAutostart{}, &fakeLogger{}).
			Create(context.Background(), validSpec())
		if err == nil {
			t.Fatal("Create = nil error, want one")
		}
		if got.Root == "" {
			t.Error("root is empty, want the dataset that was cloned and not removed")
		}
		if got.FailedAt != StepConfigure {
			t.Errorf("failed_at = %q, want %q", got.FailedAt, StepConfigure)
		}
	})
}
