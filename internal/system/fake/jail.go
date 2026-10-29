package fake

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// fakeOperationTimeout has no effect. It has been keeped for shape fidelity rather than behaviour.
var fakeOperationTimeout = 150 * time.Second

// JailSystem is an in-memory jail host. It also stands in for the two ports a
// create needs -- the configuration file and rc.conf's jail_list (JD-030) --
// because in fake mode all three have to share one set of jails: a jail whose
// stanza was just written has to appear in the next listing.
type JailSystem struct {
	// The HTTP server serves requests concurrently and a create writes here,
	// so every field below is guarded. An unguarded fake is a test double
	// that lies under -race, which this project has been bitten by before.
	mu      sync.Mutex
	jails   map[string]jails.Jail
	order   []string
	configs map[string]string
	// autostart is the fake's jail_list. Nothing reports it yet; it is kept
	// so the fake cannot claim an autostart it did not record.
	autostart map[string]bool
	// epairs maps a jail name to the host end of the epair its stanza
	// claims, so the fake can hand out units that do not collide and can
	// answer a cleanup truthfully.
	epairs map[string]string
	// rules is the kernel's rctl rule list, which is where the real thing
	// keeps them: added when the jail starts, dropped when it stops, and
	// keyed to the name rather than to the jail, so they outlive it exactly
	// as measured on a real host (JD-018).
	rules   map[string][]string
	nextJID int
}

func NewJailSystem() *JailSystem {
	seed := []jails.Jail{
		{
			JID:      "123",
			Name:     "nginx",
			Status:   jails.JailStatusRunning,
			Hostname: "nginx",
			IP:       "192.0.2.11",
			Path:     "/fake/jail/nginx",
		},
		{
			JID:      "",
			Name:     "postgres",
			Status:   jails.JailStatusStopped,
			Hostname: "postgres",
			IP:       "192.0.2.122",
			Path:     "", //deliberately left empty, to test table rendering on frontend
		},
		{
			JID:      "456",
			Name:     "redis",
			Status:   jails.JailStatusRunning,
			Hostname: "redis",
			IP:       "192.0.2.233",
			Path:     "/fake/jail/redis",
		},
	}
	s := &JailSystem{
		nextJID:   457, // past the seeded JID
		jails:     make(map[string]jails.Jail, len(seed)),
		order:     make([]string, 0, len(seed)),
		configs:   map[string]string{},
		autostart: map[string]bool{},
		epairs:    map[string]string{},
		rules:     map[string][]string{},
	}
	for _, jail := range seed {
		s.jails[jail.Name] = jail
		s.order = append(s.order, jail.Name)
	}
	return s
}

// runningLocked is called with the lock held.
func (s *JailSystem) runningLocked(jail jails.Jail) jails.Jail {
	jail.Status = jails.JailStatusRunning
	if jail.JID == "" {
		jail.JID = strconv.Itoa(s.nextJID)
		s.nextJID++
	}
	return jail
}

func (s *JailSystem) List(ctx context.Context) ([]jails.Jail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]jails.Jail, 0, len(s.order))
	for _, name := range s.order {
		list = append(list, s.jails[name])
	}
	return list, nil
}

func (s *JailSystem) Start(ctx context.Context, name string) (jails.OperationResult, error) {
	s.mu.Lock()
	jail, ok := s.jails[name]
	if !ok {
		s.mu.Unlock()
		return createJailOperationResult(jails.Jail{Name: name}, "start"), jails.ErrJailNotFound
	}
	jail = s.runningLocked(jail)
	s.jails[name] = jail
	// What the stanza's exec.poststart would have done.
	if rules := rulesInConfig(s.configs[name]); len(rules) > 0 {
		s.rules[name] = rules
	}
	s.mu.Unlock()

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail, "start"), nil
}

func (s *JailSystem) Stop(ctx context.Context, name string) (jails.OperationResult, error) {
	s.mu.Lock()
	jail, ok := s.jails[name]
	if !ok {
		s.mu.Unlock()
		return createJailOperationResult(jails.Jail{Name: name}, "stop"), jails.ErrJailNotFound
	}

	jail.Status = jails.JailStatusStopped
	jail.JID = ""
	s.jails[name] = jail
	// And what its exec.prestop would have done.
	delete(s.rules, name)
	s.mu.Unlock()

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail, "stop"), nil
}

func (s *JailSystem) Restart(ctx context.Context, name string) (jails.OperationResult, error) {
	s.mu.Lock()
	jail, ok := s.jails[name]
	if !ok {
		s.mu.Unlock()
		return createJailOperationResult(jails.Jail{Name: name}, "restart"), jails.ErrJailNotFound
	}

	jail.JID = ""
	jail = s.runningLocked(jail)
	s.jails[name] = jail
	s.mu.Unlock()

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail, "restart"), nil
}

// WriteConfig records a jail's stanza, and the jail appears -- stopped -- in
// the next listing, which is what writing the file does on a real host
// (JD-004: a configured jail is discovered from its configuration).
//
// The stanza is read back rather than being handed the jail's details
// separately, so a generated file that says nothing useful produces a fake
// jail that says nothing useful, instead of one the fake invented.
func (s *JailSystem) WriteConfig(_ context.Context, name, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := "/fake/etc/jail.conf.d/" + name + ".conf"
	if _, exists := s.configs[name]; exists {
		return path, fmt.Errorf("%s already exists", path)
	}
	s.configs[name] = content

	if match := epairUnitRE.FindStringSubmatch(content); match != nil {
		s.epairs[name] = "epair" + match[1] + "a"
	}

	jail := jails.Jail{
		Name:     name,
		Status:   jails.JailStatusStopped,
		Hostname: confValue(content, "host.hostname"),
		// A vnet jail sets no ip4.addr, so it reports no address -- which is
		// what jls says about one on a real host too.
		IP:   confValue(content, "ip4.addr"),
		Path: confValue(content, "path"),
	}
	if jail.Hostname == "" {
		jail.Hostname = name
	}
	if _, known := s.jails[name]; !known {
		s.order = append(s.order, name)
	}
	s.jails[name] = jail
	return path, nil
}

// ConfigFile reports whether a stanza was written for this jail. A seeded
// jail has none, which is the fake's version of a jail declared in
// /etc/jail.conf -- so a destroy of one is refused here too.
func (s *JailSystem) ConfigFile(_ context.Context, name string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.configs[name]
	return "/fake/etc/jail.conf.d/" + name + ".conf", exists, nil
}

// RemoveConfig deletes the stanza, and the jail leaves the listing with it --
// a configured jail is discovered from its configuration (JD-004), so a fake
// where unconfiguring changed nothing would let a broken destroy pass.
func (s *JailSystem) RemoveConfig(_ context.Context, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.configs, name)
	delete(s.jails, name)
	delete(s.epairs, name)
	delete(s.rules, name)
	s.order = slices.DeleteFunc(s.order, func(listed string) bool { return listed == name })
	return "/fake/etc/jail.conf.d/" + name + ".conf", nil
}

// ReadConfig hands back the stanza WriteConfig recorded, which is what the
// comparison behind the idempotent PUT reads (JD-016).
func (s *JailSystem) ReadConfig(_ context.Context, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, written := s.configs[name]
	if !written {
		return "", fmt.Errorf("/fake/etc/jail.conf.d/%s.conf: no such file", name)
	}
	return content, nil
}

// Autostarts reads the fake's jail_list back.
func (s *JailSystem) Autostarts(_ context.Context, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.autostart[name], nil
}

// Bridges is the operator-owned bridge a vnet jail can ask for. One, so a
// spec naming any other bridge is refused the way a real host would refuse
// it (JD-017).
func (s *JailSystem) Bridges(context.Context) ([]string, error) {
	return []string{"bridge0"}, nil
}

// FreeEpairUnit hands out the lowest unit no stanza here already claims,
// which is the same question the real adapter asks of ifconfig and of
// jail.conf.d.
func (s *JailSystem) FreeEpairUnit(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	taken := map[string]bool{}
	for _, content := range s.configs {
		for _, match := range epairUnitRE.FindAllStringSubmatch(content, -1) {
			taken[match[1]] = true
		}
	}
	for unit := 0; ; unit++ {
		if number := strconv.Itoa(unit); !taken[number] {
			return "epair" + number, nil
		}
	}
}

// RemoveJailEpair destroys the epair the jail's stanza claims, found the way
// the real adapter finds it: by the description, not by the name.
func (s *JailSystem) RemoveJailEpair(_ context.Context, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, configured := s.configs[name]
	if !configured || !strings.Contains(content, "descr jail:"+name) {
		return "", nil
	}
	iface, claimed := s.epairs[name]
	if !claimed {
		return "", nil
	}
	delete(s.epairs, name)
	return iface, nil
}

var epairUnitRE = regexp.MustCompile(`\bepair(\d+)`)

// RacctEnabled: the fake host accounts, so a spec with limits is not refused
// for the host's sake. There is a readiness check for the other answer.
func (s *JailSystem) RacctEnabled(context.Context) (bool, error) { return true, nil }

// JailRules reports what the kernel would be holding for this name.
func (s *JailSystem) JailRules(_ context.Context, name string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rules[name]), nil
}

func (s *JailSystem) RemoveJailRules(_ context.Context, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rules, name)
	return "rctl -r jail:" + name, nil
}

// DevfsRulesetNumbers are the sets a stock FreeBSD kernel loads from
// /etc/defaults/devfs.rules, so a spec naming 4 works here and one naming 99
// is refused the way a real host would refuse it (JD-018).
func (s *JailSystem) DevfsRulesetNumbers(context.Context) ([]int, error) {
	return []int{1, 2, 3, 4, 5}, nil
}

// rctlRuleRE picks the rules out of a stanza's exec.poststart lines, which
// is the same place the real jail(8) finds them.
var rctlRuleRE = regexp.MustCompile(`rctl -a (jail:[^"\s]+)`)

func rulesInConfig(content string) []string {
	matches := rctlRuleRE.FindAllStringSubmatch(content, -1)
	rules := make([]string, 0, len(matches))
	for _, match := range matches {
		rules = append(rules, match[1])
	}
	return rules
}

func (s *JailSystem) SetAutostart(_ context.Context, name string, enabled bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operator := "-="
	if enabled {
		operator = "+="
		s.autostart[name] = true
	} else {
		delete(s.autostart, name)
	}
	return "sysrc jail_list" + operator + name, nil
}

// confValue pulls one setting out of a stanza. A stand-in for the real
// adapter's parser, which reads the file the same way after it is written --
// enough to tell a usable stanza from an empty one, and deliberately not a
// second implementation of jail.conf(5).
func confValue(content, key string) string {
	match := regexp.MustCompile(
		`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*"?([^";]*)"?\s*;`).FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func createJailOperationResult(jail jails.Jail, op string) jails.OperationResult {
	opResult := jails.OperationResult{
		Jail:    jail,
		Command: createFakeCommand(jail.Name, op).String(),
	}
	return opResult
}

func createFakeCommand(name, op string) system.Command {
	return system.Command{
		Name:    "service",
		Args:    []string{"jail", op, name},
		Timeout: fakeOperationTimeout,
	}
}

var (
	_ jails.JailSystem     = (*JailSystem)(nil)
	_ jails.ConfigWriter   = (*JailSystem)(nil)
	_ jails.ConfigRemover  = (*JailSystem)(nil)
	_ jails.Autostarter    = (*JailSystem)(nil)
	_ jails.BridgeSource   = (*JailSystem)(nil)
	_ jails.EpairAllocator = (*JailSystem)(nil)
	_ jails.EpairCleaner   = (*JailSystem)(nil)
)

// ExecInJail answers a few commands the dev server can usefully show and
// echoes the rest. Not a shell: the point of the fake here is that the
// request shape, the refusals and the response shape can be exercised
// without a FreeBSD host, not that commands actually run.
func (s *JailSystem) ExecInJail(_ context.Context, name string, argv []string,
	env map[string]string, timeout time.Duration) (jails.ExecResult, error) {

	s.mu.Lock()
	_, known := s.jails[name]
	s.mu.Unlock()
	if !known {
		return jails.ExecResult{ExitCode: -1}, jails.ErrJailNotFound
	}

	result := jails.ExecResult{DurationMS: 12}
	switch {
	case argv[0] == "false":
		result.ExitCode = 1
		result.Stderr = "false: exit 1\n"
	case argv[0] == "sleep":
		// Long enough to be stopped by any sensible timeout, so the
		// timed-out path is reachable from the dev server.
		result.ExitCode = -1
		result.TimedOut = true
		result.DurationMS = timeout.Milliseconds()
	case argv[0] == "env":
		for _, variable := range sortedEnvNames(env) {
			result.Stdout += variable + "=" + env[variable] + "\n"
		}
		result.Stdout += "HOME=/root\nPATH=/sbin:/bin:/usr/sbin:/usr/bin\nUSER=root\n"
	case argv[0] == "yes":
		// Past the cap, so a truncated answer can be seen.
		result.Stdout = strings.Repeat("y\n", jails.MaxExecOutput/2+16)[:jails.MaxExecOutput]
		result.StdoutTruncated = true
	default:
		result.Stdout = strings.Join(argv, " ") + "\n"
	}
	return result, nil
}

func sortedEnvNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// AddRule puts a rule in the fake kernel the way rctl does: one for the same
// jail, resource and action replaces the one there (#119, measured).
func (s *JailSystem) AddRule(_ context.Context, rule string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, key, ok := ruleKey(rule)
	if !ok {
		return "rctl -a " + rule, fmt.Errorf("rctl: %q is not a rule", rule)
	}
	kept := slices.DeleteFunc(slices.Clone(s.rules[name]), func(have string) bool {
		_, haveKey, _ := ruleKey(have)
		return haveKey == key
	})
	s.rules[name] = append(kept, rule)
	return "rctl -a " + rule, nil
}

// RemoveRule takes out the rules a jail:<name>:<resource>:<action> filter
// matches; none is not an error, as on the real host.
func (s *JailSystem) RemoveRule(_ context.Context, filter string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, _, _ := ruleKey(filter + "=")
	s.rules[name] = slices.DeleteFunc(s.rules[name], func(have string) bool {
		return strings.HasPrefix(have, filter+"=")
	})
	return "rctl -r " + filter, nil
}

// SetCPUSet only answers: the fake has no scheduler to pin.
func (s *JailSystem) SetCPUSet(_ context.Context, name, list string) (string, error) {
	if list == "" {
		list = "all"
	}
	return "cpuset -l " + list + " -j " + name, nil
}

// RuleUsage is a small jail's worth of use, under any limit a test is likely
// to set.
func (s *JailSystem) RuleUsage(context.Context, string) (map[string]int64, error) {
	return map[string]int64{"memoryuse": 48 << 20, "vmemoryuse": 512 << 20, "maxproc": 12, "openfiles": 96}, nil
}

// ReplaceConfig rewrites a stanza WriteConfig recorded, while it still says
// old.
func (s *JailSystem) ReplaceConfig(_ context.Context, name, old, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := "/fake/etc/jail.conf.d/" + name + ".conf"
	if have, ok := s.configs[name]; !ok || have != old {
		return path, fmt.Errorf("%s changed while Jail Deck was changing it", path)
	}
	s.configs[name] = content
	return path, nil
}

// ruleKey splits jail:<name>:<resource>:<action>=<amount> into the name and
// the part a replacing rule shares.
func ruleKey(rule string) (name, key string, ok bool) {
	subject, _, hasAmount := strings.Cut(rule, "=")
	parts := strings.Split(subject, ":")
	if !hasAmount || len(parts) != 4 || parts[0] != "jail" {
		return "", "", false
	}
	return parts[1], subject, true
}
