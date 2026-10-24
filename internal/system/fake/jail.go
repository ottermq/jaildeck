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
	epairs  map[string]string
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
	s.order = slices.DeleteFunc(s.order, func(listed string) bool { return listed == name })
	return "/fake/etc/jail.conf.d/" + name + ".conf", nil
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
