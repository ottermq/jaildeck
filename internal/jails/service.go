package jails

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
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
	// datasets may be nil, which means no storage source is wired and every
	// listing reports jails without their datasets. Only tests that are not
	// about storage pass nil; the application always wires one.
	datasets DatasetSource
}

func NewJailService(system JailSystem, operationLogger audit.Logger, datasets DatasetSource) *JailService {
	return &JailService{
		system:          system,
		operationLogger: operationLogger,
		datasets:        datasets,
	}
}

func (s *JailService) List(ctx context.Context) ([]Jail, error) {
	jails, err := s.system.List(ctx)
	if err != nil {
		return nil, err
	}
	return s.withStorage(ctx, jails), nil
}

// Get returns one jail by name, with its storage, or ErrJailNotFound. It reads
// the same merged listing rather than asking the system for a single jail:
// JD-004 discovers a stopped jail from configuration and a running one from
// jls, and a jail present in only one of them must still be found here.
func (s *JailService) Get(ctx context.Context, name JailName) (Jail, error) {
	if _, err := NewJailName(name.String()); err != nil {
		return Jail{}, err
	}
	jails, err := s.system.List(ctx)
	if err != nil {
		return Jail{}, toListError(err)
	}
	for _, jail := range jails {
		if jail.Name == name.String() {
			// One jail, so the storage read is scoped to its path alone.
			return s.withStorage(ctx, []Jail{jail})[0], nil
		}
	}
	return Jail{}, ErrJailNotFound
}

// withStorage fills in each jail's datasets and user properties from one read
// of the host, and hands the jails back unchanged if that read fails.
func (s *JailService) withStorage(ctx context.Context, jails []Jail) []Jail {
	if s.datasets == nil {
		return jails
	}
	paths := make([]string, 0, len(jails))
	for _, jail := range jails {
		// A jail with no path -- a stopped jail whose stanza sets none -- is
		// not on ZFS as far as this can tell, and an empty path would
		// otherwise prefix-match every dataset on the host.
		if jail.Path != "" {
			paths = append(paths, jail.Path)
		}
	}
	if len(paths) == 0 {
		return jails
	}

	storage, err := s.datasets.StorageForPaths(ctx, paths)
	if err != nil {
		// Degraded deliberately rather than failed. The jail listing is how an
		// operator sees a stopped jail in order to start it (JD-004), and an
		// unreadable pool must not take that away. The cost is that a jail
		// then looks like one with no datasets, which is why the reason is
		// logged rather than swallowed.
		common.Logger(ctx, "jail").Warn("listing jails without their datasets",
			slog.Int("jails", len(jails)),
			slog.Any("err", err))
		return jails
	}
	for i, jail := range jails {
		if found, ok := storage[jail.Path]; ok {
			jails[i].Datasets = found.Datasets
			jails[i].Properties = found.Properties
		}
	}
	return jails
}

// toListError keeps a failed listing from reaching the client as a bare Go
// error, the way toAppError does for an operation.
func toListError(err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	msg := err.Error()
	var cmdErr *system.CommandError
	if errors.As(err, &cmdErr) {
		msg = cmdErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError, fmt.Sprintf("failed to list jails: %s", msg))
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
		common.Logger(ctx, "jail").Error("failed to write operation log",
			// common.NewComponentLogger("jail").Error("failed to write operation log",
			slog.String("operation", operation.String()),
			slog.String("target", name),
			slog.Any("err", err))
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
