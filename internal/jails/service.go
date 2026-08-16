package jails

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

var (
	ErrInvalidJailName = common.NewAppError(http.StatusBadRequest, "invalid jail name")
)

type JailName string

func NewJailName(name string) (JailName, error) {
	if name == "" {
		return "", ErrInvalidJailName
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return "", common.NewAppError(http.StatusBadRequest, fmt.Sprintf("invalid jail name %q", name))
		}
	}
	return JailName(name), nil
}

func (n JailName) String() string {
	return string(n)
}

type JailService struct {
	system          JailSystem
	operationLogger audit.Logger
}

func NewJailService(system JailSystem, operationLogger audit.Logger) *JailService {
	return &JailService{
		system:          system,
		operationLogger: operationLogger,
	}
}

func (s *JailService) List(ctx context.Context) ([]Jail, error) {
	jails, err := s.system.List(ctx)
	if err != nil {
		return nil, err
	}
	return jails, nil
}

func (s *JailService) do(ctx context.Context, name JailName, action string, call func(context.Context, string) (Jail, error)) (Jail, error) {
	if _, err := NewJailName(name.String()); err != nil {
		return Jail{}, err
	}
	jail, err := call(ctx, name.String())
	s.logJailOperation(ctx, name.String(), action, err)
	return jail, err
}

func (s *JailService) Start(ctx context.Context, name JailName) (Jail, error) {
	return s.do(ctx, name, "start", s.system.Start)
}

func (s *JailService) Stop(ctx context.Context, name JailName) (Jail, error) {
	return s.do(ctx, name, "stop", s.system.Stop)
}

func (s *JailService) Restart(ctx context.Context, name JailName) (Jail, error) {
	return s.do(ctx, name, "restart", s.system.Restart)
}

func (s *JailService) logJailOperation(ctx context.Context, name, operation string, opErr error) {
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: operation,
		Target:    name,
		Command:   fmt.Sprintf("service jail %s %s", operation, name), // TODO: replace hardcoded for actual command (from system)
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
