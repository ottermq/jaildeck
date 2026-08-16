package fake

import (
	"context"
	"fmt"

	"github.com/ottermq/jaildeck/internal/jails"
)

type JailSystem struct {
	jails map[string]jails.Jail
}

func NewJailSystem() *JailSystem {
	return &JailSystem{
		jails: map[string]jails.Jail{
			"nginx":    {Name: "nginx", Status: jails.JailStatusRunning},
			"postgres": {Name: "postgres", Status: jails.JailStatusStopped},
			"redis":    {Name: "redis", Status: jails.JailStatusRunning},
		},
	}
}

func (s *JailSystem) List(ctx context.Context) ([]jails.Jail, error) {
	return []jails.Jail{
		s.jails["nginx"],
		s.jails["postgres"],
		s.jails["redis"],
	}, nil
}

func (s *JailSystem) Start(ctx context.Context, name string) (jails.Jail, error) {
	jail, ok := s.jails[name]
	if !ok {
		return jails.Jail{}, fmt.Errorf("jail %q not found", name)
	}

	jail.Status = jails.JailStatusRunning
	s.jails[name] = jail

	return jail, nil
}

func (s *JailSystem) Stop(ctx context.Context, name string) (jails.Jail, error) {
	jail, ok := s.jails[name]
	if !ok {
		return jails.Jail{}, fmt.Errorf("jail %q not found", name)
	}

	jail.Status = jails.JailStatusStopped
	s.jails[name] = jail

	return jail, nil
}

func (s *JailSystem) Restart(ctx context.Context, name string) (jails.Jail, error) {
	jail, ok := s.jails[name]
	if !ok {
		return jails.Jail{}, fmt.Errorf("jail %q not found", name)
	}

	jail.Status = jails.JailStatusRunning
	s.jails[name] = jail

	return jail, nil
}
