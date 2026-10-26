package vms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/system"
)

// The audit log's names for what the lifecycle runs. Prefixed, because a jail
// and a guest can have the same name and the entries share one log.
const (
	OperationStart    = "vm-start"
	OperationStop     = "vm-stop"
	OperationPoweroff = "vm-poweroff"
)

// How long each step is given to show in vm-bhyve's listing (JD-036).
//
// Measured on isengard: a started guest is Running within a second, and a
// stopped or powered-off one is Stopped within one -- vm stop itself waits a
// second between its two signals. A booted Debian took 4-5 seconds to answer
// the power button (JD-019). StopTimeout is the operating system's time to
// shut down, which is the guest's and not vm-bhyve's, so it is generous.
const (
	StartTimeout    = 15 * time.Second
	StopTimeout     = 60 * time.Second
	PoweroffTimeout = 15 * time.Second
	pollInterval    = time.Second
)

// Start starts a guest and confirms vm-bhyve lists it as running.
//
// A guest already running, or in its boot loader, is left alone and answered
// as it is: idempotent, as a jail's start is. One locked by another host is
// refused. Nothing here is cancelled by the client going away; each step is
// bounded by its own timeout.
func (s *Service) Start(ctx context.Context, name string) (OperationResult, error) {
	if s.system == nil {
		return OperationResult{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)
	vm, err := s.guest(ctx, name)
	if err != nil {
		return OperationResult{}, err
	}
	switch vm.State {
	case StateRunning, StateBootloader:
		return OperationResult{VM: *vm}, nil
	case StateLocked:
		return OperationResult{}, locked(vm)
	}
	return s.start(ctx, name)
}

// Stop stops a guest: the power button first, and if the guest has not
// stopped within StopTimeout, `vm poweroff`. The answer says which (JD-019,
// JD-036).
//
// A stopped or suspended guest is left alone. One in its boot loader is
// refused: vm-bhyve's stop asks on its terminal before killing a loader, and
// with no terminal it declines without saying so -- read in its source.
func (s *Service) Stop(ctx context.Context, name string) (OperationResult, error) {
	if s.system == nil {
		return OperationResult{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)
	vm, err := s.guest(ctx, name)
	if err != nil {
		return OperationResult{}, err
	}
	switch vm.State {
	case StateStopped, StateSuspended:
		return OperationResult{VM: *vm}, nil
	case StateLocked:
		return OperationResult{}, locked(vm)
	case StateBootloader:
		return OperationResult{}, inBootloader(name)
	}
	return s.stop(ctx, name)
}

// Restart is Stop, then Start. vm-bhyve's own restart only asks the guest to
// reboot through the power button, and has no deadline.
func (s *Service) Restart(ctx context.Context, name string) (OperationResult, error) {
	if s.system == nil {
		return OperationResult{}, s.unavailable()
	}
	ctx = context.WithoutCancel(ctx)
	vm, err := s.guest(ctx, name)
	if err != nil {
		return OperationResult{}, err
	}
	switch vm.State {
	case StateLocked:
		return OperationResult{}, locked(vm)
	case StateBootloader:
		return OperationResult{}, inBootloader(name)
	}
	forced := false
	if vm.State == StateRunning {
		stopped, err := s.stop(ctx, name)
		if err != nil {
			return OperationResult{}, err
		}
		forced = stopped.Forced
	}
	started, err := s.start(ctx, name)
	if err != nil {
		return OperationResult{}, err
	}
	started.Forced = forced
	return started, nil
}

func (s *Service) start(ctx context.Context, name string) (OperationResult, error) {
	command, err := s.system.VMStart(ctx, name)
	if err == nil {
		var vm VM
		var up bool
		vm, up, err = s.await(ctx, name, StartTimeout, StateRunning, StateBootloader)
		if err == nil && !up {
			err = fmt.Errorf("vm start returned, and the guest was not running %s later; "+
				"vm-bhyve's log is vm-bhyve.log in the guest's directory", StartTimeout)
		}
		if err == nil {
			s.log(ctx, OperationStart, name, command, nil)
			return OperationResult{VM: vm}, nil
		}
	}
	s.log(ctx, OperationStart, name, command, err)
	return OperationResult{}, operationError("start", name, err)
}

func (s *Service) stop(ctx context.Context, name string) (OperationResult, error) {
	command, err := s.system.VMStop(ctx, name)
	if err != nil {
		s.log(ctx, OperationStop, name, command, err)
		return OperationResult{}, operationError("stop", name, err)
	}
	vm, down, err := s.await(ctx, name, StopTimeout, StateStopped, StateSuspended)
	if err != nil {
		s.log(ctx, OperationStop, name, command, err)
		return OperationResult{}, operationError("stop", name, err)
	}
	if down {
		s.log(ctx, OperationStop, name, command, nil)
		return OperationResult{VM: vm}, nil
	}
	s.log(ctx, OperationStop, name, command,
		fmt.Errorf("the guest did not answer the ACPI shutdown within %s, and was powered off", StopTimeout))

	command, err = s.system.VMPoweroff(ctx, name)
	if err == nil {
		vm, down, err = s.await(ctx, name, PoweroffTimeout, StateStopped, StateSuspended)
		if err == nil && !down {
			err = fmt.Errorf("vm poweroff returned, and the guest was still listed as %s %s later", vm.State, PoweroffTimeout)
		}
	} else if now, lookErr := s.guest(ctx, name); lookErr == nil && (now.State == StateStopped || now.State == StateSuspended) {
		// It stopped between the deadline and the poweroff, which vm-bhyve
		// then refuses: "doesn't appear to be a running virtual machine".
		// The guest is stopped, which is what was asked.
		s.log(ctx, OperationPoweroff, name, command, err)
		return OperationResult{VM: *now}, nil
	}
	s.log(ctx, OperationPoweroff, name, command, err)
	if err != nil {
		return OperationResult{}, operationError("power off", name, err)
	}
	return OperationResult{VM: vm, Forced: true}, nil
}

// await looks at vm-bhyve's listing once a second until the guest is in one
// of the states, or until the timeout. It answers the guest's last row and
// whether it got there.
func (s *Service) await(ctx context.Context, name string, timeout time.Duration, states ...string) (VM, bool, error) {
	var vm VM
	for waited := time.Duration(0); ; waited += pollInterval {
		row, err := s.guest(ctx, name)
		if err != nil {
			return VM{}, false, err
		}
		vm = *row
		for _, state := range states {
			if vm.State == state {
				return vm, true, nil
			}
		}
		if waited >= timeout {
			return vm, false, nil
		}
		s.wait(ctx, pollInterval)
	}
}

func (s *Service) log(ctx context.Context, operation, name, command string, opErr error) {
	if s.audit == nil {
		return
	}
	entry := audit.Entry{
		Timestamp: time.Now(),
		Operation: operation,
		Target:    name,
		Command:   command,
		Success:   opErr == nil,
	}
	if opErr != nil {
		entry.TimedOut = errors.Is(opErr, context.DeadlineExceeded)
		entry.ExitCode = -1
		entry.Error = opErr.Error()
		var commandErr *system.CommandError
		if errors.As(opErr, &commandErr) {
			entry.ExitCode = commandErr.Result.ExitCode
			entry.Error = commandErr.Unwrap().Error()
		}
	}
	if err := s.audit.Log(ctx, entry); err != nil {
		common.Logger(ctx, "vms").Error("failed to write operation log",
			slog.String("operation", operation), slog.String("target", name), slog.Any("err", err))
	}
}

func locked(vm *VM) error {
	return common.NewAppError(http.StatusConflict, fmt.Sprintf(
		"%s is locked by %s: it is marked as running on that host, in a datastore the two share",
		strconv.Quote(vm.Name), vm.LockedBy))
}

func inBootloader(name string) error {
	return common.NewAppError(http.StatusConflict, strconv.Quote(name)+
		" is still in its boot loader, which vm-bhyve will not stop without asking on a terminal; try again once it is running")
}

func operationError(verb, name string, err error) error {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return common.NewAppError(http.StatusGatewayTimeout,
			fmt.Sprintf("%s of %s timed out and may not have completed", verb, strconv.Quote(name)))
	}
	message := err.Error()
	var commandErr *system.CommandError
	if errors.As(err, &commandErr) {
		message = commandErr.Summary()
	}
	return common.NewAppError(http.StatusInternalServerError,
		fmt.Sprintf("could not %s %s: %s", verb, strconv.Quote(name), message))
}
