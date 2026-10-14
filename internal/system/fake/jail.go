package fake

import (
	"context"
	"strconv"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
)

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
		return jails.OperationResult{}, jails.ErrJailNotFound
	}
	jail = s.running(jail)
	s.jails[name] = jail

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail), nil
}

func (s *JailSystem) Stop(ctx context.Context, name string) (jails.OperationResult, error) {
	jail, ok := s.jails[name]
	if !ok {
		return jails.OperationResult{}, jails.ErrJailNotFound
	}

	jail.Status = jails.JailStatusStopped
	jail.JID = ""
	s.jails[name] = jail

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail), nil
}

func (s *JailSystem) Restart(ctx context.Context, name string) (jails.OperationResult, error) {
	jail, ok := s.jails[name]
	if !ok {
		return jails.OperationResult{}, jails.ErrJailNotFound
	}

	jail.JID = ""
	jail = s.running(jail)
	s.jails[name] = jail

	time.Sleep(3 * time.Second)
	return createJailOperationResult(jail), nil
}

func createJailOperationResult(jail jails.Jail) jails.OperationResult {
	opResult := jails.OperationResult{
		Jail:    jail,
		Command: "",
	}
	return opResult
}
