package services

import (
	"context"
	"fmt"

	"github.com/otterlabs/jaildeck/internal/domain"
	"github.com/otterlabs/jaildeck/internal/system"
)

type JailService struct {
	system system.JailSystem
}

func NewJailService(system system.JailSystem) *JailService {
	return &JailService{
		system: system,
	}
}

func (s *JailService) List(ctx context.Context) ([]domain.Jail, error) {
	return s.system.List(ctx)
}

func (s *JailService) Start(ctx context.Context, name string) (domain.Jail, error) {
	if !validJailName(name) {
		return domain.Jail{}, fmt.Errorf("invalid jail name %q", name)
	}
	return s.system.Start(ctx, name)
}

func (s *JailService) Stop(ctx context.Context, name string) (domain.Jail, error) {
	if !validJailName(name) {
		return domain.Jail{}, fmt.Errorf("invalid jail name %q", name)
	}
	return s.system.Stop(ctx, name)
}

func (s *JailService) Restart(ctx context.Context, name string) (domain.Jail, error) {
	if !validJailName(name) {
		return domain.Jail{}, fmt.Errorf("invalid jail name %q", name)
	}
	return s.system.Restart(ctx, name)
}

func validJailName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' || r == '.' {
			continue
		}

		return false
	}
	return true
}
