package jails

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
)

// --- the rewrite --------------------------------------------------------

// A rewritten stanza is exactly the one a create with the new limits writes,
// in every network mode and between every shape of limits. That is what keeps
// a jail whose limits were changed matching its new spec under PUT (JD-016).
func TestRewriteHooksMatchesTheGenerator(t *testing.T) {
	shapes := []Limits{
		{},
		{CPUSet: "2"},
		{MemoryUse: "640M"},
		{MemoryUse: "640M", MaxProc: 100, CPUSet: "0-3"},
		{MemoryUse: "1G", VMemoryUse: "2G", MaxProc: 200, OpenFiles: 4096, PCPU: 150, CPUSet: "4-7"},
	}
	specs := map[string]Spec{"alias": validSpec(), "vnet": vnetSpec()}
	inherit := validSpec()
	inherit.Network = Network{Mode: NetworkInherit}
	specs["inherit"] = inherit

	for mode, spec := range specs {
		at := Placement{Path: "/usr/local/jails/containers/db1", Epair: "epair3"}
		for _, from := range shapes {
			for _, to := range shapes {
				spec.Limits = from
				before, err := spec.Stanza(at)
				if err != nil {
					t.Fatal(err)
				}
				spec.Limits = to
				want, _ := spec.Stanza(at)
				got, err := rewriteHooks(before, to, spec.Name)
				if err != nil {
					t.Fatalf("%s %+v -> %+v: %v", mode, from, to, err)
				}
				if got != want {
					t.Errorf("%s %+v -> %+v:\n got:\n%s\nwant:\n%s", mode, from, to, got, want)
				}
			}
		}
	}
}

func TestRewriteHooksReplacesTheOldHeader(t *testing.T) {
	spec := limitedSpec()
	current, _ := spec.Stanza(Placement{Path: "/p"})
	old := strings.Replace(current, confHeader, headerBefore, 1)
	if old == current {
		t.Fatal("the test did not put the old header in")
	}
	got, err := rewriteHooks(old, spec.Limits, spec.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Errorf("got:\n%s\nwant:\n%s", got, current)
	}
	if strings.Contains(got, "does not rewrite this file") {
		t.Error("the rewritten stanza still promises it is never rewritten")
	}
}

// A stanza by hand keeps everything that is not a hook, comments included.
func TestRewriteHooksKeepsEverythingElse(t *testing.T) {
	stanza := `db1 {
	# the operator's note
	path = "/p";
	allow.raw_sockets;
	exec.poststart = "rctl -a jail:db1:memoryuse:deny=1G";
	exec.prestop = "rctl -r jail:db1";
}
`
	got, err := rewriteHooks(stanza, Limits{MaxProc: 50}, "db1")
	if err != nil {
		t.Fatal(err)
	}
	want := `db1 {
	# the operator's note
	path = "/p";
	allow.raw_sockets;
	exec.poststart = "rctl -a jail:db1:maxproc:deny=50";
	exec.prestop = "rctl -r jail:db1";
}
`
	// The hand-written stanza had no blank line before its hooks, and the
	// rewrite adds the generator's.
	want = strings.Replace(want, "allow.raw_sockets;\n", "allow.raw_sockets;\n\n", 1)
	want = strings.Replace(want, "deny=50\";\n", "deny=50\";\n\n", 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteHooksNeedsABrace(t *testing.T) {
	_, err := rewriteHooks("db1 {\n\tpath = \"/p\";\n", Limits{MaxProc: 5}, "db1")
	assertStatus(t, err, http.StatusConflict)
}

// --- reading them back --------------------------------------------------

func TestLimitsInReadsWhatTheGeneratorWrote(t *testing.T) {
	for _, limits := range []Limits{
		{},
		{CPUSet: "0-3,8"},
		{MemoryUse: "640M", VMemoryUse: "1G", MaxProc: 100, OpenFiles: 2048, PCPU: 100, CPUSet: "0-3"},
	} {
		spec := vnetSpec()
		spec.Limits = limits
		stanza, _ := spec.Stanza(Placement{Path: "/p", Epair: "epair1"})
		got, err := limitsIn(stanza, spec.Name)
		if err != nil {
			t.Fatal(err)
		}
		if got != limits {
			t.Errorf("read %+v back as %+v", limits, got)
		}
	}
}

func TestLimitsInRefusesHooksItDidNotWrite(t *testing.T) {
	for _, hooks := range []string{
		`exec.poststart = "/usr/local/bin/notify started";`,
		`exec.poststart = "rctl -a jail:db1:memoryuse:deny=1G"; exec.poststart += "rctl -a jail:db1:memoryuse:deny=2G";`,
		`exec.poststart = "rctl -a jail:db10:memoryuse:deny=1G";`,
		`exec.poststart = "rctl -a jail:db1:memoryuse:log=1G";`,
		`exec.poststart = "rctl -a jail:db1:maxproc:deny=lots";`,
		`exec.prestop = "rctl -r jail:db1:memoryuse";`,
		`exec.prestop = "service postgresql stop";`,
	} {
		stanza := "db1 {\n\tpath = \"/p\";\n\t" + strings.ReplaceAll(hooks, "; ", ";\n\t") + "\n}\n"
		_, err := limitsIn(stanza, "db1")
		assertStatus(t, err, http.StatusConflict)
	}
}

// --- the patch ----------------------------------------------------------

func patchOf(t *testing.T, body string) LimitsPatch {
	t.Helper()
	var patch LimitsPatch
	if err := json.Unmarshal([]byte(body), &patch); err != nil {
		t.Fatal(err)
	}
	return patch
}

func TestLimitsPatchApply(t *testing.T) {
	current := Limits{MemoryUse: "640M", MaxProc: 100, CPUSet: "0-3"}
	tests := []struct {
		body string
		want Limits
	}{
		{`{"memoryuse":"1G"}`, Limits{MemoryUse: "1G", MaxProc: 100, CPUSet: "0-3"}},
		{`{"memoryuse":null}`, Limits{MaxProc: 100, CPUSet: "0-3"}},
		{`{"cpuset":null,"pcpu":150}`, Limits{MemoryUse: "640M", MaxProc: 100, PCPU: 150}},
		{`{"memoryuse":" 2g "}`, Limits{MemoryUse: "2g", MaxProc: 100, CPUSet: "0-3"}},
		{`{"openfiles":null}`, current},
	}
	for _, test := range tests {
		got, err := patchOf(t, test.body).apply(current)
		if err != nil {
			t.Errorf("%s: %v", test.body, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s: got %+v, want %+v", test.body, got, test.want)
		}
	}
}

func TestLimitsPatchRefuses(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"memory":"1G"}`,
		`{"maxproc":0}`,
		`{"memoryuse":""}`,
		`{"memoryuse":"   "}`,
		`{"maxproc":"100"}`,
		`{"maxproc":-1}`,
		`{"memoryuse":"640X"}`,
		`{"cpuset":"3-1"}`,
	} {
		_, err := patchOf(t, body).apply(Limits{MaxProc: 10})
		assertStatus(t, err, http.StatusBadRequest)
	}
	// A zero is told how to remove a limit, not just that it is wrong.
	_, err := patchOf(t, `{"maxproc":0}`).apply(Limits{MaxProc: 10})
	if err == nil || err.Error() != "limits.maxproc is 0; to remove the limit, send null" {
		t.Errorf("got %v", err)
	}
}

// --- the service --------------------------------------------------------

type stubLimits struct {
	steps *[]string
	// failOn are the commands that fail, matched by substring.
	failOn   []string
	usage    map[string]int64
	usageErr error
}

func (s *stubLimits) run(command string) (string, error) {
	*s.steps = append(*s.steps, command)
	for _, fail := range s.failOn {
		if strings.Contains(command, fail) {
			return command, errors.New("rctl: Invalid argument")
		}
	}
	return command, nil
}

func (s *stubLimits) AddRule(_ context.Context, rule string) (string, error) {
	return s.run("rctl -a " + rule)
}

func (s *stubLimits) RemoveRule(_ context.Context, filter string) (string, error) {
	return s.run("rctl -r " + filter)
}

func (s *stubLimits) SetCPUSet(_ context.Context, name, list string) (string, error) {
	if list == "" {
		list = "all"
	}
	return s.run("cpuset -l " + list + " -j " + name)
}

func (s *stubLimits) RuleUsage(context.Context, string) (map[string]int64, error) {
	return s.usage, s.usageErr
}

type stubReplacer struct {
	config *stubConfig
	steps  *[]string
	err    error
}

func (s *stubReplacer) ReplaceConfig(_ context.Context, name, old, content string) (string, error) {
	*s.steps = append(*s.steps, "rewrite")
	if s.err != nil {
		return "/etc/jail.conf.d/" + name + ".conf", s.err
	}
	if s.config.written[name] != old {
		return "", errors.New("changed underneath")
	}
	s.config.written[name] = content
	return "/etc/jail.conf.d/" + name + ".conf", nil
}

type limitsHost struct {
	service  *JailService
	steps    []string
	config   *stubConfig
	limits   *stubLimits
	replacer *stubReplacer
	rctl     *stubRctl
	logger   *fakeLogger
}

// newLimitsHost is db1 written with limitedSpec's limits, running unless
// told otherwise.
func newLimitsHost(t *testing.T, status JailStatus) *limitsHost {
	t.Helper()
	h := &limitsHost{logger: &fakeLogger{}}
	spec := limitedSpec()
	stanza, err := spec.Stanza(Placement{Path: clonedRoot().Mountpoint})
	if err != nil {
		t.Fatal(err)
	}
	h.config = &stubConfig{written: map[string]string{"db1": stanza}}
	h.limits = &stubLimits{steps: &h.steps, usage: map[string]int64{"memoryuse": 300 << 20, "maxproc": 40}}
	h.replacer = &stubReplacer{config: h.config, steps: &h.steps}
	h.rctl = &stubRctl{enabled: true, rules: map[string][]string{}}
	h.service = NewJailService(Dependencies{
		System: listSystem{jails: []Jail{{Name: "db1", Status: status, Path: clonedRoot().Mountpoint}}},
		Audit:  h.logger, Rctl: h.rctl,
		ConfigReader: &stubConfigReader{config: h.config},
		Limits:       h.limits, ConfigReplacer: h.replacer,
	})
	return h
}

func (h *limitsHost) set(t *testing.T, body string) (LimitsChange, error) {
	t.Helper()
	return h.service.SetLimits(context.Background(), JailName("db1"), patchOf(t, body))
}

func TestSetLimitsOnARunningJail(t *testing.T) {
	h := newLimitsHost(t, JailStatusRunning)
	change, err := h.set(t, `{"memoryuse":"1G","maxproc":null,"pcpu":200,"cpuset":"0-7"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"rctl -a jail:db1:memoryuse:deny=1G",
		"rctl -r jail:db1:maxproc:deny",
		"rctl -a jail:db1:pcpu:deny=200",
		"cpuset -l 0-7 -j db1",
		"rewrite",
	}
	if !slices.Equal(h.steps, want) {
		t.Errorf("steps %q, want %q", h.steps, want)
	}
	wantLimits := Limits{MemoryUse: "1G", PCPU: 200, CPUSet: "0-7"}
	if !change.Changed || !change.Running || change.Limits != wantLimits {
		t.Errorf("answered %+v", change)
	}
	// The stanza is now what a create with those limits writes.
	spec := limitedSpec()
	spec.Limits = wantLimits
	generated, _ := spec.Stanza(Placement{Path: clonedRoot().Mountpoint})
	if h.config.written["db1"] != generated {
		t.Errorf("stanza:\n%s\nwant:\n%s", h.config.written["db1"], generated)
	}
	operations := []string{}
	for _, entry := range h.logger.entries {
		operations = append(operations, entry.Operation)
		if !entry.Success {
			t.Errorf("audited a failure: %+v", entry)
		}
	}
	wantOps := []string{OperationAddRule, OperationRemoveRule, OperationAddRule, OperationSetCPUSet, OperationRewriteConfig}
	if !slices.Equal(operations, wantOps) {
		t.Errorf("audited %q, want %q", operations, wantOps)
	}
}

func TestSetLimitsAgainChangesNothing(t *testing.T) {
	h := newLimitsHost(t, JailStatusRunning)
	if _, err := h.set(t, `{"memoryuse":"1G"}`); err != nil {
		t.Fatal(err)
	}
	h.steps, h.logger.entries = nil, nil
	change, err := h.set(t, `{"memoryuse":"1G"}`)
	if err != nil {
		t.Fatal(err)
	}
	if change.Changed || len(h.steps) != 0 || len(h.logger.entries) != 0 {
		t.Errorf("a repeat changed something: %+v, steps %q", change, h.steps)
	}
}

func TestSetLimitsOnAStoppedJailWritesOnlyTheStanza(t *testing.T) {
	h := newLimitsHost(t, JailStatusStopped)
	h.rctl.rules["db1"] = []string{"jail:db1:openfiles:deny=10"}
	change, err := h.set(t, `{"maxproc":50}`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.steps, []string{"rewrite"}) {
		t.Errorf("steps %q, want the rewrite alone", h.steps)
	}
	if !change.Changed || change.Running || change.Limits.MaxProc != 50 {
		t.Errorf("answered %+v", change)
	}
	if len(change.Warnings) != 1 || !strings.Contains(change.Warnings[0], "rctl -r jail:db1") {
		t.Errorf("leftover rules not warned about: %q", change.Warnings)
	}
}

// A kernel step that fails takes back the ones before it, newest first, and
// the stanza is never written.
func TestSetLimitsTakesBackAFailedChange(t *testing.T) {
	h := newLimitsHost(t, JailStatusRunning)
	h.limits.failOn = []string{"cpuset -l 99"}
	_, err := h.set(t, `{"memoryuse":"1G","pcpu":200,"cpuset":"99"}`)
	if err == nil || !strings.Contains(err.Error(), "the host refused cpuset -l 99 -j db1: rctl: Invalid argument") ||
		!strings.Contains(err.Error(), "put back, so db1 runs as it did") {
		t.Fatalf("got %v", err)
	}
	want := []string{
		"rctl -a jail:db1:memoryuse:deny=1G",
		"rctl -a jail:db1:pcpu:deny=200",
		"cpuset -l 99 -j db1",
		"rctl -r jail:db1:pcpu:deny",
		"rctl -a jail:db1:memoryuse:deny=640M",
	}
	if !slices.Equal(h.steps, want) {
		t.Errorf("steps %q, want %q", h.steps, want)
	}
	if !strings.Contains(h.config.written["db1"], "deny=640M") {
		t.Error("the stanza changed although the kernel refused")
	}
}

func TestSetLimitsTakesBackWhenTheStanzaCannotBeWritten(t *testing.T) {
	h := newLimitsHost(t, JailStatusRunning)
	h.replacer.err = common.NewAppError(http.StatusConflict, "changed while Jail Deck was changing it")
	_, err := h.set(t, `{"memoryuse":"1G"}`)
	assertStatus(t, err, http.StatusConflict)
	want := []string{"rctl -a jail:db1:memoryuse:deny=1G", "rewrite", "rctl -a jail:db1:memoryuse:deny=640M"}
	if !slices.Equal(h.steps, want) {
		t.Errorf("steps %q, want %q", h.steps, want)
	}
}

func TestSetLimitsSaysWhenTakingBackFailed(t *testing.T) {
	h := newLimitsHost(t, JailStatusRunning)
	// The cpuset is refused, and putting maxproc back to 100 fails too.
	h.limits.failOn = []string{"cpuset", "maxproc:deny=100"}
	_, err := h.set(t, `{"maxproc":200,"cpuset":"5"}`)
	if err == nil || !strings.Contains(err.Error(), "runs under limits its stanza does not carry") ||
		!strings.Contains(err.Error(), "rctl -a jail:db1:maxproc:deny=100") {
		t.Errorf("got %v", err)
	}
}

func TestSetLimitsWarnsOfALimitAlreadyExceeded(t *testing.T) {
	h := newLimitsHost(t, JailStatusRunning)
	change, err := h.set(t, `{"memoryuse":"256M","maxproc":20}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Warnings) != 2 ||
		!strings.Contains(change.Warnings[0], "300.0M of resident memory") ||
		!strings.Contains(change.Warnings[1], "runs 40 processes") {
		t.Errorf("warnings %q", change.Warnings)
	}
	// A raised one is not warned about.
	h = newLimitsHost(t, JailStatusRunning)
	if change, _ := h.set(t, `{"memoryuse":"4G"}`); len(change.Warnings) != 0 {
		t.Errorf("warned of a raise: %q", change.Warnings)
	}
}

func TestSetLimitsRefuses(t *testing.T) {
	t.Run("a stanza with a hook Jail Deck did not write", func(t *testing.T) {
		h := newLimitsHost(t, JailStatusRunning)
		h.config.written["db1"] = strings.Replace(h.config.written["db1"], "}\n",
			"\texec.poststart += \"/usr/local/bin/notify\";\n}\n", 1)
		_, err := h.set(t, `{"memoryuse":"1G"}`)
		assertStatus(t, err, http.StatusConflict)
		if len(h.steps) != 0 {
			t.Errorf("ran %q", h.steps)
		}
	})
	t.Run("a jail configured in jail.conf", func(t *testing.T) {
		h := newLimitsHost(t, JailStatusRunning)
		delete(h.config.written, "db1")
		_, err := h.set(t, `{"memoryuse":"1G"}`)
		assertStatus(t, err, http.StatusConflict)
	})
	t.Run("rctl limits on a host without accounting", func(t *testing.T) {
		h := newLimitsHost(t, JailStatusRunning)
		h.rctl.enabled = false
		_, err := h.set(t, `{"memoryuse":"1G"}`)
		assertStatus(t, err, http.StatusBadRequest)
		if len(h.steps) != 0 {
			t.Errorf("ran %q", h.steps)
		}
	})
	t.Run("an unknown jail", func(t *testing.T) {
		h := newLimitsHost(t, JailStatusRunning)
		_, err := h.service.SetLimits(context.Background(), JailName("db2"), patchOf(t, `{"maxproc":5}`))
		if !errors.Is(err, ErrJailNotFound) {
			assertStatus(t, err, http.StatusNotFound)
		}
	})
	t.Run("a Jail Deck that cannot rewrite", func(t *testing.T) {
		service := NewJailService(Dependencies{System: listSystem{}})
		_, err := service.SetLimits(context.Background(), JailName("db1"), patchOf(t, `{"maxproc":5}`))
		assertStatus(t, err, http.StatusNotImplemented)
	})
}

func TestAmountBytes(t *testing.T) {
	for amount, want := range map[string]int64{"1G": 1 << 30, "640m": 640 << 20, "512": 512, "2T": 2 << 40} {
		if got, ok := amountBytes(amount); !ok || got != want {
			t.Errorf("%s: %d, want %d", amount, got, want)
		}
	}
	if _, ok := amountBytes("lots"); ok {
		t.Error("read lots as a size")
	}
}

func assertStatus(t *testing.T, err error, want int) {
	t.Helper()
	var appErr *common.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("error is %v (%T), want an AppError with %d", err, err, want)
	}
	if appErr.Code != want {
		t.Errorf("status %d, want %d: %s", appErr.Code, want, appErr.Message)
	}
}
