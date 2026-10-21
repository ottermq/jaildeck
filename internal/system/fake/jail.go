package fake

import (
	"context"
	"strconv"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
)

// fakeOperationTimeout has no effect. It has been keeped for shape fidelity rather than behaviour.
var fakeOperationTimeout = 150 * time.Second

type JailSystem struct {
	jails   map[string]jails.Jail
	nextJID int
}

func NewJailSystem() *JailSystem {
	return &JailSystem{
		nextJID: 457, // past the seeded JID
		jails: map[string]jails.Jail{
			"nginx": {
				JID:      "123",
				Name:     "nginx",
				Status:   jails.JailStatusRunning,
				Hostname: "nginx",
				IP:       "192.0.2.11",
				Path:     "/fake/jail/nginx",
			},
			"postgres": {
				JID:      "",
				Name:     "postgres",
				Status:   jails.JailStatusStopped,
				Hostname: "postgres",
				IP:       "192.0.2.122",
				Path:     "", //deliberately left empty, to test table rendering on frontend
			},
			"redis": {
				JID:      "456",
				Name:     "redis",
				Status:   jails.JailStatusRunning,
				Hostname: "redis",
				IP:       "192.0.2.233",
				Path:     "/fake/jail/redis",
			},
		},
	}
}

func (s *JailSystem) running(jail jails.Jail) jails.Jail {
	jail.Status = jails.JailStatusRunning
	if jail.JID == "" {
		jail.JID = strconv.Itoa(s.nextJID)
		s.nextJID++
	}
	return jail
}

func (s *JailSystem) List(ctx context.Context) ([]jails.Jail, error) {
	return []jails.Jail{
		s.jails["nginx"],
		s.jails["postgres"],
		s.jails["redis"],
	}, nil
}

func (s *JailSystem) Start(ctx context.Context, name string) (jails.OperationResult, error) {
	jail, ok := s.jails[name]
	if !ok {
		return createJailOperationResult(jails.Jail{Name: name}, "start"), jails.ErrJailNotFound
	}
	jail = s.running(jail)
	s.jails[name] = jail

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail, "start"), nil
}

func (s *JailSystem) Stop(ctx context.Context, name string) (jails.OperationResult, error) {
	jail, ok := s.jails[name]
	if !ok {
		return createJailOperationResult(jails.Jail{Name: name}, "stop"), jails.ErrJailNotFound
	}

	jail.Status = jails.JailStatusStopped
	jail.JID = ""
	s.jails[name] = jail

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail, "stop"), nil
}

func (s *JailSystem) Restart(ctx context.Context, name string) (jails.OperationResult, error) {
	jail, ok := s.jails[name]
	if !ok {
		return createJailOperationResult(jails.Jail{Name: name}, "restart"), jails.ErrJailNotFound
	}

	jail.JID = ""
	jail = s.running(jail)
	s.jails[name] = jail

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail, "restart"), nil
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
