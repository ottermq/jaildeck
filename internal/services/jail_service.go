package services

import (
	"context"

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

func (s *JailService) List(ctx context.Context) ([]system.Jail, error) {
	return s.system.List(ctx)
}

func (s *JailService) Start(ctx context.Context, name string) (system.Jail, error) {
	return s.system.Start(ctx, name)
}

func (s *JailService) Stop(ctx context.Context, name string) (system.Jail, error) {
	return s.system.Stop(ctx, name)
}

func (s *JailService) Restart(ctx context.Context, name string) (system.Jail, error) {
	return s.system.Restart(ctx, name)
}
