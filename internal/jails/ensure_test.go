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

// stubConfigReader reads back what stubConfig wrote, so a test can create a
// jail through the service and then ask whether it matches the spec that
// made it. That is the whole question this endpoint answers, and a double
// that answered from anywhere else would not be asking it.
type stubConfigReader struct {
	config *stubConfig
	// edited stands in for a file somebody changed after Jail Deck wrote it.
	edited map[string]string
	absent bool
	err    error
}

func (s *stubConfigReader) ConfigFile(_ context.Context, name string) (string, bool, error) {
	path := "/etc/jail.conf.d/" + name + ".conf"
	if s.absent {
		return path, false, nil
	}
	_, written := s.content(name)
	return path, written, nil
}

func (s *stubConfigReader) ReadConfig(_ context.Context, name string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	content, written := s.content(name)
	if !written {
		return "", errors.New("/etc/jail.conf.d/" + name + ".conf: no such file")
	}
	return content, nil
}

func (s *stubConfigReader) content(name string) (string, bool) {
	if content, edited := s.edited[name]; edited {
		return content, true
	}
	content, written := s.config.written[name]
	return content, written
}

type stubAutostartSource struct {
	autostart *stubAutostart
	err       error
}

func (s *stubAutostartSource) Autostarts(_ context.Context, name string) (bool, error) {
	return s.autostart.set[name], s.err
}

type stubOrigin struct {
	dataset string
	origin  string
	err     error
	asked   []string
}

func (s *stubOrigin) RootOrigin(_ context.Context, jailPath string) (string, string, error) {
	s.asked = append(s.asked, jailPath)
	return s.dataset, s.origin, s.err
}

var (
	_ ConfigReader    = (*stubConfigReader)(nil)
	_ AutostartSource = (*stubAutostartSource)(nil)
	_ CloneOrigin     = (*stubOrigin)(nil)
)

// ensureHost is a whole host: the doubles a create needs, plus the three a
// comparison reads back through. They share state, so a jail created here
// really is there for the next call.
type ensureHost struct {
	system    *createSystem
	cloner    *stubCloner
	config    *stubConfig
	autostart *stubAutostart
	logger    *fakeLogger
	rctl      *stubRctl
	devfs     *stubDevfs
	bridges   *stubBridges
	epairs    *stubEpairs
	cleaner   *stubEpairCleaner
	reader    *stubConfigReader
	starts    *stubAutostartSource
	origin    *stubOrigin
}

func newEnsureHost() *ensureHost {
	system := &createSystem{}
	config := &stubConfig{path: "/etc/jail.conf.d/db1.conf", system: system}
	autostart := &stubAutostart{}
	return &ensureHost{
		system: system, config: config, autostart: autostart,
		cloner:  &stubCloner{root: clonedRoot()},
		logger:  &fakeLogger{},
		rctl:    accountingHost(),
		devfs:   &stubDevfs{loaded: []int{1, 2, 3, 4, 5}},
		bridges: &stubBridges{have: []string{"bridge0"}},
		epairs:  &stubEpairs{unit: "epair7"},
		cleaner: &stubEpairCleaner{},
		reader:  &stubConfigReader{config: config},
		starts:  &stubAutostartSource{autostart: autostart},
		origin:  &stubOrigin{dataset: clonedRoot().Dataset, origin: validSpec().Template},
	}
}

func (h *ensureHost) service() *JailService {
	return NewJailService(Dependencies{
		System: h.system, Audit: h.logger,
		RootCloner: h.cloner, Config: h.config, Autostart: h.autostart,
		Bridges: h.bridges, Epairs: h.epairs, Epair: h.cleaner,
		Rctl: h.rctl, DevfsRulesets: h.devfs,
		ConfigReader: h.reader, AutostartSource: h.starts, CloneOrigin: h.origin,
	})
}

// --- the contract ------------------------------------------------------

func TestEnsureCreatesAJailThatIsNotThere(t *testing.T) {
	host := newEnsureHost()

	result, err := host.service().Ensure(context.Background(), "db1", validSpec())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !result.Changed {
		t.Error("changed = false for a jail that had to be created")
	}
	if len(result.Differences) != 0 {
		t.Errorf("differences = %v on a create", result.Differences)
	}
	if result.Root != "tank/jails/containers/db1" || !result.Running {
		t.Errorf("result = %+v, want the create's own report", result.Created)
	}
}

// The acceptance criterion: repeating the call changes nothing and says so.
//
// Each spec goes through the whole create -- generator included -- and then
// through the comparison, so the two halves are held against each other
// rather than against a fixture somebody wrote by hand.
func TestEnsureRepeatedIsANoOp(t *testing.T) {
	tests := []struct {
		name string
		spec func() Spec
	}{
		{"an alias jail", validSpec},
		{"a jail that is not started", func() Spec {
			spec := validSpec()
			no := false
			spec.Start = &no
			return spec
		}},
		{"an autostarting jail", func() Spec {
			spec := validSpec()
			spec.Autostart = true
			return spec
		}},
		{"a vnet jail", vnetSpec},
		{"a jail with limits and parameters", func() Spec {
			spec := limitedSpec()
			spec.Params = map[string]any{"devfs_ruleset": 4.0, "sysvshm": "new", "allow.chflags": true}
			return spec
		}},
		{"a vnet jail with limits", func() Spec {
			spec := vnetSpec()
			spec.Limits = Limits{MemoryUse: "512M", CPUSet: "0-1"}
			return spec
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newEnsureHost()
			service := host.service()

			first, err := service.Ensure(context.Background(), "db1", test.spec())
			if err != nil {
				t.Fatalf("the first Ensure: %v", err)
			}
			if !first.Changed {
				t.Fatal("the first Ensure changed nothing")
			}

			second, err := service.Ensure(context.Background(), "db1", test.spec())
			if err != nil {
				t.Fatalf("the second Ensure: %v", err)
			}
			if second.Changed {
				t.Error("the second Ensure changed something")
			}
			if len(second.Differences) != 0 {
				t.Errorf("the same spec differs from the jail it made: %+v", second.Differences)
			}
			// Nothing was written or cloned twice.
			if len(host.cloner.cloned) != 1 {
				t.Errorf("cloned %v", host.cloner.cloned)
			}
			if n := len(host.system.started); n > 1 {
				t.Errorf("started the jail %d times", n)
			}
		})
	}
}

// A vnet jail's stanza names a unit allocated when it was created. The
// comparison reads that unit back out of the file rather than asking for
// another -- one that would differ from the file and that nothing would use.
func TestEnsureReusesTheEpairTheStanzaClaims(t *testing.T) {
	host := newEnsureHost()
	service := host.service()

	if _, err := service.Ensure(context.Background(), "db1", vnetSpec()); err != nil {
		t.Fatalf("the first Ensure: %v", err)
	}
	host.epairs.unit = "epair99" // a second allocation would hand out this

	second, err := service.Ensure(context.Background(), "db1", vnetSpec())
	if err != nil {
		t.Fatalf("the second Ensure: %v", err)
	}
	if len(second.Differences) != 0 {
		t.Errorf("the vnet jail differs from the spec that made it: %+v", second.Differences)
	}
	if host.epairs.call != 1 {
		t.Errorf("allocated %d epairs, want 1: a comparison allocates nothing", host.epairs.call)
	}
}

func TestEnsureRefusesAJailThatDiffers(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
		have  string
		// change turns the created spec into the one that is asked for the
		// second time.
		change func(*Spec)
		// host, when set, changes the world instead of the spec.
		host func(*ensureHost)
	}{
		{
			name: "a different hostname", field: "host.hostname",
			want: "other", have: "db1",
			change: func(s *Spec) { s.Hostname = "other" },
		},
		{
			name: "a different address", field: "ip4.addr",
			want: "192.0.2.99/24", have: "192.0.2.10/24",
			change: func(s *Spec) { s.Network.IP4 = "192.0.2.99/24" },
		},
		{
			name: "a different interface", field: "interface",
			want: "em1", have: "em0",
			change: func(s *Spec) { s.Network.Interface = "em1" },
		},
		{
			name: "a different network mode", field: "ip4",
			want: "disable", have: "",
			change: func(s *Spec) { s.Network = Network{Mode: NetworkDisable} },
		},
		{
			name: "a parameter that was not set", field: "devfs_ruleset",
			want: "4", have: "",
			change: func(s *Spec) { s.Params = map[string]any{"devfs_ruleset": 4.0} },
		},
		{
			name: "a different limit", field: "exec.poststart",
			want: "rctl -a jail:db1:memoryuse:deny=640M", have: "",
			change: func(s *Spec) { s.Limits = Limits{MemoryUse: "640M"} },
		},
		{
			name: "a different template", field: "template",
			want:   "tank/jails/templates/15.2-RELEASE@base",
			have:   "tank/jails/templates/15.1-RELEASE@base",
			change: func(s *Spec) { s.Template = "tank/jails/templates/15.2-RELEASE@base" },
		},
		{
			name: "autostart turned on", field: "autostart",
			want: "true", have: "false",
			change: func(s *Spec) { s.Autostart = true },
		},
		{
			// jls reports where the jail actually is; the stanza says where
			// it should be. A file edited since the jail started is a jail
			// that is not what the spec describes.
			name: "a path the stanza no longer agrees with", field: "path",
			want: "/usr/local/jails/containers/db1", have: "/elsewhere",
			host: func(h *ensureHost) {
				h.edit("db1", func(content string) string {
					return strings.Replace(content,
						`path = "/usr/local/jails/containers/db1";`,
						`path = "/elsewhere";`, 1)
				})
			},
		},
		{
			// Jail Deck never rewrites this file, so anything added to it is
			// kept (JD-030) -- and is a difference, because the jail is no
			// longer the one the spec describes.
			name: "a setting somebody added by hand", field: "allow.raw_sockets",
			want: "", have: "set",
			host: func(h *ensureHost) {
				h.edit("db1", func(content string) string {
					return strings.Replace(content, "\tmount.devfs;",
						"\tmount.devfs;\n\tallow.raw_sockets;", 1)
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newEnsureHost()
			service := host.service()
			if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
				t.Fatalf("the create: %v", err)
			}

			spec := validSpec()
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
			if result.Changed {
				t.Error("changed = true: a jail that differs is left alone")
			}
			found := false
			for _, difference := range result.Differences {
				if difference.Field != test.field {
					continue
				}
				found = true
				if !strings.Contains(difference.Want, test.want) {
					t.Errorf("want = %q, should mention %q", difference.Want, test.want)
				}
				if !strings.Contains(difference.Have, test.have) {
					t.Errorf("have = %q, should mention %q", difference.Have, test.have)
				}
			}
			if !found {
				t.Errorf("no difference named %q; got %+v", test.field, result.Differences)
			}
			// Left exactly as it was: no second write, no start, no stop.
			if len(host.cloner.cloned) != 1 || len(host.system.stopped) != 0 {
				t.Errorf("the host was touched: cloned %v, stopped %v",
					host.cloner.cloned, host.system.stopped)
			}
		})
	}
}

// The order is documented and a client reads it: the template the jail came
// from, then the settings of its configuration as they sort, then whether it
// comes back after a reboot.
func TestEnsureReportsDifferencesInAFixedOrder(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
		t.Fatalf("the create: %v", err)
	}

	spec := validSpec()
	spec.Template = "tank/jails/templates/15.2-RELEASE@base"
	spec.Hostname = "other"
	spec.Autostart = true
	spec.Network.IP4 = "192.0.2.99/24"
	spec.Params = map[string]any{"devfs_ruleset": 4.0}

	result, err := service.Ensure(context.Background(), "db1", spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	got := make([]string, 0, len(result.Differences))
	for _, difference := range result.Differences {
		got = append(got, difference.Field)
	}
	want := []string{"template", "devfs_ruleset", "host.hostname", "ip4.addr", "autostart"}
	if !slices.Equal(got, want) {
		t.Errorf("differences = %v, want %v", got, want)
	}
}

// edit replaces the stanza on disk, the way an operator with an editor would.
func (h *ensureHost) edit(name string, change func(string) string) {
	if h.reader.edited == nil {
		h.reader.edited = map[string]string{}
	}
	h.reader.edited[name] = change(h.config.written[name])
}

// None of these is a difference in the jail. A stanza an older Jail Deck
// wrote has its lines in another order, and jail(8) reads a value the same
// quoted or not.
func TestEnsureIgnoresWhatIsNotADifference(t *testing.T) {
	tests := []struct {
		name    string
		rewrite func(string) string
	}{
		{"a comment", func(c string) string {
			return c + "\n# an operator left a note\n"
		}},
		{"a blank line", func(c string) string {
			return strings.Replace(c, "\tmount.devfs;", "\n\tmount.devfs;\n", 1)
		}},
		{"different quoting", func(c string) string {
			return strings.Replace(c, `host.hostname = "db1";`, "host.hostname = db1;", 1)
		}},
		{"a different line order", func(c string) string {
			// Two settings swapped, which is what an older Jail Deck's
			// stanza looks like next to today's.
			stop := "\texec.stop = \"/bin/sh /etc/rc.shutdown\";\n"
			console := "\texec.consolelog = \"/var/log/jail_console_${name}.log\";\n"
			return strings.Replace(c, stop+console, console+stop, 1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newEnsureHost()
			service := host.service()
			if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
				t.Fatalf("the create: %v", err)
			}
			before := host.config.written["db1"]
			host.edit("db1", test.rewrite)
			if host.reader.edited["db1"] == before {
				t.Fatalf("%s changed nothing in the stanza, so this proves nothing", test.name)
			}

			result, err := service.Ensure(context.Background(), "db1", validSpec())
			if err != nil {
				t.Fatalf("Ensure: %v", err)
			}
			if len(result.Differences) != 0 {
				t.Errorf("%s was reported as a difference: %+v", test.name, result.Differences)
			}
		})
	}
}

// --- refusals ----------------------------------------------------------

func TestEnsureRefusesASpecForAnotherJail(t *testing.T) {
	host := newEnsureHost()
	spec := validSpec()
	spec.Name = "db2"

	_, err := host.service().Ensure(context.Background(), "db1", spec)
	if err == nil {
		t.Fatal("want a refusal for a spec naming another jail")
	}
	if code := appCode(t, err); code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", code, http.StatusBadRequest)
	}
	if len(host.cloner.cloned) != 0 {
		t.Errorf("cloned %v", host.cloner.cloned)
	}
}

// The URL has already named it, so the body need not.
func TestEnsureTakesTheNameFromTheURL(t *testing.T) {
	host := newEnsureHost()
	spec := validSpec()
	spec.Name = ""

	result, err := host.service().Ensure(context.Background(), "db1", spec)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if result.Jail.Name != "db1" {
		t.Errorf("name = %q, want db1", result.Jail.Name)
	}
}

func TestEnsureRefusesAJailItCannotUnconfigure(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
		t.Fatalf("the create: %v", err)
	}
	host.reader.absent = true

	_, err := service.Ensure(context.Background(), "db1", validSpec())
	if err == nil {
		t.Fatal("want a refusal for a jail with no file of its own")
	}
	if code := appCode(t, err); code != http.StatusConflict {
		t.Errorf("status = %d, want %d", code, http.StatusConflict)
	}
	if !strings.Contains(err.Error(), "/etc/jail.conf") {
		t.Errorf("error = %q, want it to name where the block lives", err)
	}
}

// A path no dataset is mounted at cannot be compared against a template.
func TestEnsureReportsAnUnreadableRoot(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
		t.Fatalf("the create: %v", err)
	}
	host.origin.err = ErrJailNotOnZFS

	if _, err := service.Ensure(context.Background(), "db1", validSpec()); !errors.Is(err, ErrJailNotOnZFS) {
		t.Fatalf("err = %v, want ErrJailNotOnZFS", err)
	}
}

func TestEnsureRefusesAJailWithNoPath(t *testing.T) {
	host := newEnsureHost()
	host.system.existing = []Jail{{Name: "db1", Status: JailStatusStopped}}
	host.config.written = map[string]string{"db1": "db1 {\n}\n"}

	_, err := host.service().Ensure(context.Background(), "db1", validSpec())
	if err == nil {
		t.Fatal("want a refusal for a jail with no path")
	}
	if code := appCode(t, err); code != http.StatusConflict {
		t.Errorf("status = %d, want %d", code, http.StatusConflict)
	}
}

// A comparison that cannot read one of the three would answer "it matches"
// without having looked, so the ports are required rather than optional.
func TestEnsureNeedsWhatItReadsBackThrough(t *testing.T) {
	for _, missing := range []string{"config", "origin", "autostart"} {
		host := newEnsureHost()
		service := host.service()
		if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
			t.Fatalf("the create: %v", err)
		}
		deps := Dependencies{
			System: host.system, Audit: host.logger,
			RootCloner: host.cloner, Config: host.config, Autostart: host.autostart,
			ConfigReader: host.reader, AutostartSource: host.starts, CloneOrigin: host.origin,
		}
		switch missing {
		case "config":
			deps.ConfigReader = nil
		case "origin":
			deps.CloneOrigin = nil
		case "autostart":
			deps.AutostartSource = nil
		}

		_, err := NewJailService(deps).Ensure(context.Background(), "db1", validSpec())
		if err == nil {
			t.Fatalf("no %s port: want a refusal", missing)
		}
		if code := appCode(t, err); code != http.StatusNotImplemented {
			t.Errorf("no %s port: status = %d, want %d", missing, code, http.StatusNotImplemented)
		}
	}
}

// A create that stopped half way changed the host, and a client told
// "nothing changed" would be misled about the one case where it matters.
func TestEnsureReportsAPartialCreate(t *testing.T) {
	host := newEnsureHost()
	host.system.startErr = errors.New("jail status is 'stopped', want 'running'")

	result, err := host.service().Ensure(context.Background(), "db1", validSpec())
	if err == nil {
		t.Fatal("want the start's error")
	}
	if !result.Changed {
		t.Error("changed = false after a create that cloned and configured")
	}
	if result.FailedAt != StepStart {
		t.Errorf("failed_at = %q, want %q", result.FailedAt, StepStart)
	}
	if result.Root == "" {
		t.Error("the body does not say what exists")
	}
}

// ...and a create refused before anything was made did not change the host,
// which is the answer a client retrying after a timeout is relying on.
func TestEnsureChangedIsFalseWhenNothingWasCreated(t *testing.T) {
	host := newEnsureHost()
	host.cloner.checkErr = common.NewAppError(http.StatusNotFound, "no snapshot named that")

	result, err := host.service().Ensure(context.Background(), "db1", validSpec())
	if err == nil {
		t.Fatal("want the check's refusal")
	}
	if result.Changed {
		t.Error("changed = true after a create that was refused before it made anything")
	}
	if result.Root != "" {
		t.Errorf("root = %q, want empty", result.Root)
	}
	if len(host.cloner.cloned) != 0 {
		t.Errorf("cloned %v", host.cloner.cloned)
	}
}

// A hand-written stanza routinely says ${name} where a generated one says
// the name. jail(8) expands it, so it is the same jail -- and reporting it
// as a difference would send an operator to fix something that is correct.
func TestEnsureExpandsNameTheWayJailDoes(t *testing.T) {
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
		t.Fatalf("the create: %v", err)
	}
	host.edit("db1", func(content string) string {
		content = strings.Replace(content, `host.hostname = "db1";`, `host.hostname = "${name}";`, 1)
		return strings.Replace(content,
			`path = "/usr/local/jails/containers/db1";`,
			`path = "/usr/local/jails/containers/$name";`, 1)
	})

	result, err := service.Ensure(context.Background(), "db1", validSpec())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(result.Differences) != 0 {
		t.Errorf("$name was read as a difference: %+v", result.Differences)
	}
}

// --- reading a stanza back ---------------------------------------------

func TestSettingsIn(t *testing.T) {
	settings := settingsIn(`# a note
db1 {
	exec.clean;
	host.hostname = "db1";   # another note
	exec.poststart = "one";
	exec.poststart += "two";
	ip4 = disable;
	ip4 = inherit;
	exec.consolelog = "/var/log/jail_console_${name}.log";
	exec.poststop = "logger $namespace done";
}
`, "db1")
	for _, test := range []struct {
		name string
		want []string
	}{
		{"exec.clean", []string{""}},
		{"host.hostname", []string{"db1"}},
		// += appends and = replaces, which is how jail(8) reads the two.
		{"exec.poststart", []string{"one", "two"}},
		{"ip4", []string{"inherit"}},
		// $name is what jail(8) would have expanded before the kernel saw
		// it, so a stanza that writes it says the same thing as one that
		// does not.
		{"exec.consolelog", []string{"/var/log/jail_console_db1.log"}},
		// $name ends at a word boundary: $namespace is a different
		// variable, and rewriting half of it would invent a value.
		{"exec.poststop", []string{"logger $namespace done"}},
	} {
		got := settings[test.name]
		if len(got) != len(test.want) {
			t.Errorf("%s = %v, want %v", test.name, got, test.want)
			continue
		}
		for i := range got {
			if got[i] != test.want[i] {
				t.Errorf("%s = %v, want %v", test.name, got, test.want)
				break
			}
		}
	}
	// The block's own braces are not settings, and nor is the comment.
	for _, absent := range []string{"db1", "}", "#"} {
		if _, found := settings[absent]; found {
			t.Errorf("%q was read as a setting", absent)
		}
	}
}

// A line this does not understand is kept rather than dropped: silently
// losing one is how a jail that does not match is reported as matching.
func TestSettingsInKeepsWhatItCannotParse(t *testing.T) {
	settings := settingsIn("db1 {\n\tthis is not jail.conf\n}\n", "db1")
	if _, found := settings["this is not jail.conf"]; !found {
		t.Errorf("an unparsed line was dropped: %v", settings)
	}
}

func TestEpairIn(t *testing.T) {
	for _, test := range []struct{ content, want string }{
		{`vnet.interface = "epair7b";`, "epair7"},
		{"exec.prestart = \"ifconfig epair0 create\";", "epair0"},
		{"ip4 = disable;", ""},
	} {
		if got := epairIn(test.content); got != test.want {
			t.Errorf("epairIn(%q) = %q, want %q", test.content, got, test.want)
		}
	}
}
