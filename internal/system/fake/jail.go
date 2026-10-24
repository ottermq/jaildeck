package fake

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
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
	nextJID   int
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

	jail := jails.Jail{
		Name:     name,
		Status:   jails.JailStatusStopped,
		Hostname: confValue(content, "host.hostname"),
		IP:       confValue(content, "ip4.addr"),
		Path:     confValue(content, "path"),
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
	_ jails.JailSystem   = (*JailSystem)(nil)
	_ jails.ConfigWriter = (*JailSystem)(nil)
	_ jails.Autostarter  = (*JailSystem)(nil)
)
