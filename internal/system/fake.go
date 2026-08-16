package system

import (
	"context"
	"fmt"
)

type FakeJailSystem struct {
	jails map[string]Jail
}

func NewFakeJailSystem() *FakeJailSystem {
	return &FakeJailSystem{
		jails: map[string]Jail{
			"nginx":    {Name: "nginx", Status: JailStatusRunning},
			"postgres": {Name: "postgres", Status: JailStatusStopped},
			"redis":    {Name: "redis", Status: JailStatusRunning},
		},
	}
}

func (s *FakeJailSystem) List(ctx context.Context) ([]Jail, error) {
	return []Jail{
		s.jails["nginx"],
		s.jails["postgres"],
		s.jails["redis"],
	}, nil
}

func (s *FakeJailSystem) Start(ctx context.Context, name string) (Jail, error) {
	jail, ok := s.jails[name]
	if !ok {
		return Jail{}, fmt.Errorf("jail %q not found", name)
	}

	jail.Status = JailStatusRunning
	s.jails[name] = jail

	return jail, nil
}

func (s *FakeJailSystem) Stop(ctx context.Context, name string) (Jail, error) {
	jail, ok := s.jails[name]
	if !ok {
		return Jail{}, fmt.Errorf("jail %q not found", name)
	}

	jail.Status = JailStatusStopped
	s.jails[name] = jail

	return jail, nil
}

func (s *FakeJailSystem) Restart(ctx context.Context, name string) (Jail, error) {
	jail, ok := s.jails[name]
	if !ok {
		return Jail{}, fmt.Errorf("jail %q not found", name)
	}

	jail.Status = JailStatusRunning
	s.jails[name] = jail

	return jail, nil
}
