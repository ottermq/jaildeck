package jails

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
	"codeberg.org/Otter-Labs/jaildeck/internal/common"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
)

var (
	ErrInvalidJailName = common.NewAppError(http.StatusBadRequest, "invalid jail name")
	ErrJailNotFound    = common.NewAppError(http.StatusNotFound, "jail not found")
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

func (s *JailService) do(ctx context.Context, name JailName, op Operation, call func(context.Context, string) (OperationResult, error)) (OperationResult, error) {
	// A client disconnecting must not abort a jail operation half-way, so drop
	// the request's cancellation. The runner still enforces the command timeout.
	opCtx := context.WithoutCancel(ctx)
	if _, err := NewJailName(name.String()); err != nil {
		return OperationResult{}, err
	}
	result, err := call(opCtx, name.String())
	s.logJailOperation(opCtx, name.String(), op, result.Command, err)
	return result, toAppError(name.String(), op, err)
}

func (s *JailService) Start(ctx context.Context, name JailName) (OperationResult, error) {
	return s.do(ctx, name, OperationStart, s.system.Start)
}

func (s *JailService) Stop(ctx context.Context, name JailName) (OperationResult, error) {
	return s.do(ctx, name, OperationStop, s.system.Stop)
}

func (s *JailService) Restart(ctx context.Context, name JailName) (OperationResult, error) {
	return s.do(ctx, name, OperationRestart, s.system.Restart)
}

func (s *JailService) logJailOperation(ctx context.Context, name string, operation Operation, command string, opErr error) {
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: operation.String(),
		Target:    name,
		Command:   command,
		Success:   opErr == nil,
	}

	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		var commandErr *system.CommandError
		if errors.As(opErr, &commandErr) {
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

func toAppError(name string, op Operation, err error) error {
	if err == nil {
		return nil
	}
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewAppError(http.StatusGatewayTimeout, fmt.Sprintf("%s of jail %q timed out and may not have completed", op, name))
	}
	msg := err.Error()
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		msg = cmdErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError, fmt.Sprintf("failed to %s jail %q: %s", op, name, msg))
}
