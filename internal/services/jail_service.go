package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/domain"
	"github.com/ottermq/jaildeck/internal/operations"
	"github.com/ottermq/jaildeck/internal/system"
)

type JailService struct {
	system          system.JailSystem
	operationLogger operations.Logger
}

func NewJailService(system system.JailSystem, operationLogger operations.Logger) *JailService {
	return &JailService{
		system:          system,
		operationLogger: operationLogger,
	}
}

func (s *JailService) List(ctx context.Context) ([]domain.Jail, error) {
	return s.system.List(ctx)
}

func (s *JailService) Start(ctx context.Context, name string) (domain.Jail, error) {
	if !validJailName(name) {
		return domain.Jail{}, fmt.Errorf("invalid jail name %q", name)
	}
	jail, err := s.system.Start(ctx, name)
	s.logJailOperation(ctx, name, "start", err)
	return jail, err
}

func (s *JailService) Stop(ctx context.Context, name string) (domain.Jail, error) {
	if !validJailName(name) {
		return domain.Jail{}, fmt.Errorf("invalid jail name %q", name)
	}
	jail, err := s.system.Stop(ctx, name)
	s.logJailOperation(ctx, name, "stop", err)
	return jail, err
}

func (s *JailService) Restart(ctx context.Context, name string) (domain.Jail, error) {
	if !validJailName(name) {
		return domain.Jail{}, fmt.Errorf("invalid jail name %q", name)
	}
	jail, err := s.system.Restart(ctx, name)
	s.logJailOperation(ctx, name, "restart", err)

	return jail, err
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

func (s *JailService) logJailOperation(ctx context.Context, name, operation string, opErr error) {
	entry := operations.Entry{
		Timestamp: time.Now(),
		Operation: operation,
		Target:    name,
		Command:   fmt.Sprintf("service jail %s %s", name, operation), // TODO: replace hardcoded for actual command (from system)
		Success:   opErr == nil,
	}
	if opErr != nil {
		var commandErr *system.CommandError
		if errors.As(opErr, &commandErr) {
			entry.Command = commandErr.Command + " " + strings.Join(commandErr.Args, " ")
			entry.ExitCode = commandErr.Result.ExitCode
			entry.Error = commandErr.Unwrap().Error()

		} else {
			entry.Error = opErr.Error()
			entry.ExitCode = -1
		}
	}
	if err := s.operationLogger.Log(ctx, entry); err != nil {
		log.Printf("failed to write operation log: %v", err)
	}
}
