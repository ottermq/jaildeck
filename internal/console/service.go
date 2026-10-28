package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
)

// The audit log's names for a console session (JD-042). Each session writes
// one of each: the open when the shell started, the close with how long it
// lasted and why it ended. Nothing typed and nothing printed is recorded,
// anywhere.
const (
	OperationOpen  = "console-open"
	OperationClose = "console-close"
)

// Why a session ended, as the audit log and the page are told.
const (
	ReasonClient         = "client"
	ReasonIdle           = "idle"
	ReasonSessionExpired = "session-expired"
	ReasonShellExited    = "shell-exited"
	ReasonJailStopped    = "jail-stopped"
	// A guest's console: the guest stopped, or the device itself ended.
	ReasonGuestStopped  = "guest-stopped"
	ReasonConsoleClosed = "console-closed"
)

// GuestConsole says whether a guest is running and, if it is, which device
// is the host's end of its first console port -- read from vm info, never
// taken from the request.
type GuestConsole interface {
	Console(ctx context.Context, name string) (device string, running bool, err error)
}

// guestWatch is how often an open VM console looks at whether its guest still
// runs. A powered-off guest gives the device's reader nothing at all -- no end
// of file, no error, measured -- so this is how such a session ends.
const guestWatch = 5 * time.Second

// JailState says whether a jail is running. ErrNoSuchJail-like refusals come
// back as the jails domain's own AppError and are passed on as they are.
type JailState interface {
	Running(ctx context.Context, name string) (bool, error)
}

// Service opens console sessions and keeps the ones that are open, so that a
// jail about to stop can hang them up first: an open shell made a jail stop
// take 10 s instead of under one, measured (JD-042).
type Service struct {
	shells Shells
	jails  JailState
	guests GuestConsole
	audit  audit.Logger
	now    func() time.Time
	watch  time.Duration

	mu       sync.Mutex
	sessions map[*Session]struct{}
	// vmTaken are the guests with a console open, or opening: one each.
	vmTaken map[string]bool
}

func NewService(shells Shells, jails JailState, guests GuestConsole, auditLogger audit.Logger) *Service {
	return &Service{shells: shells, jails: jails, guests: guests, audit: auditLogger, now: time.Now,
		watch: guestWatch, sessions: map[*Session]struct{}{}, vmTaken: map[string]bool{}}
}

// Session is one open console: its terminal, and how it ends.
type Session struct {
	Terminal
	target    string
	principal string
	opened    time.Time
	service   *Service

	once   sync.Once
	reason string
	ended  chan struct{}
	// outputEnded is the reason when the terminal's output ends of itself:
	// a shell that exits, a console device that closes.
	outputEnded string
	// release frees what the session holds besides its terminal: a guest's
	// one console.
	release func()
}

// OutputEnded is why the session ends when its terminal stops giving output.
func (s *Session) OutputEnded() string { return s.outputEnded }

// Ended is closed once something has ended the session from outside the
// connection: the jail stopping, or the server. Reason says why.
func (s *Session) Ended() <-chan struct{} { return s.ended }

// Reason is why the session ended, once it has.
func (s *Session) Reason() string {
	s.service.mu.Lock()
	defer s.service.mu.Unlock()
	return s.reason
}

// end hangs the terminal up and records why, once; a later reason, such as
// the read that fails because of the hang-up, does not replace the first.
func (s *Session) end(reason string) bool {
	first := false
	s.once.Do(func() {
		first = true
		s.service.mu.Lock()
		s.reason = reason
		s.service.mu.Unlock()
		close(s.ended)
		_ = s.Terminal.Close()
	})
	return first
}

// OpenJail starts a shell in a running jail and records it. The size is the
// page's terminal, so the shell's first prompt already fits it.
func (s *Service) OpenJail(ctx context.Context, name, principal string, cols, rows uint16) (*Session, error) {
	if s.shells == nil {
		return nil, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the host access a console needs")
	}
	running, err := s.jails.Running(ctx, name)
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, common.NewAppError(http.StatusConflict,
			strconv.Quote(name)+" is not running; start it to open a console in it")
	}
	target := "jail/" + name
	terminal, err := s.shells.JailShell(ctx, name, cols, rows)
	command := "jexec -l " + name + " /bin/sh -l"
	s.log(ctx, audit.Entry{Operation: OperationOpen, Target: target, Command: command,
		Principal: principal, Success: err == nil, Error: errorText(err)})
	if err != nil {
		return nil, common.NewAppError(http.StatusInternalServerError,
			fmt.Sprintf("could not open a shell in %s: %v", strconv.Quote(name), err))
	}
	session := &Session{Terminal: terminal, target: target, principal: principal,
		opened: s.now(), service: s, ended: make(chan struct{}), outputEnded: ReasonShellExited}
	s.mu.Lock()
	s.sessions[session] = struct{}{}
	s.mu.Unlock()
	return session, nil
}

// OpenVM attaches to a running guest's serial console. One session per
// guest: the device has one reader, and two sessions would each get half of
// what the guest prints. An outside `vm console` or cu can still attach --
// TIOCEXCL does not stop root (measured) -- and that is said, not prevented.
func (s *Service) OpenVM(ctx context.Context, name, principal string) (*Session, error) {
	if s.shells == nil || s.guests == nil {
		return nil, common.NewAppError(http.StatusNotImplemented,
			"this Jail Deck is running without the host access a console needs")
	}
	device, running, err := s.guests.Console(ctx, name)
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, common.NewAppError(http.StatusConflict,
			strconv.Quote(name)+" is not running; its console exists only while it does")
	}
	// nmdm creates a pair for any name it is asked to open (measured), so
	// the path has to be this guest's own, as vm-bhyve names it.
	if !nmdmDevice.MatchString(device) || !strings.HasPrefix(device, "/dev/nmdm-"+name+".") {
		return nil, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"vm info names %s as %s's console, which is not the nmdm device vm-bhyve makes", strconv.Quote(device), name))
	}
	s.mu.Lock()
	if s.vmTaken[name] {
		s.mu.Unlock()
		return nil, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"a console is already open on %s, and a guest has one; close it first", strconv.Quote(name)))
	}
	s.vmTaken[name] = true
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		delete(s.vmTaken, name)
		s.mu.Unlock()
	}

	target := "vm/" + name
	terminal, err := s.shells.VMConsole(ctx, device)
	s.log(ctx, audit.Entry{Operation: OperationOpen, Target: target, Command: "open " + device,
		Principal: principal, Success: err == nil, Error: errorText(err)})
	if err != nil {
		release()
		return nil, common.NewAppError(http.StatusInternalServerError,
			fmt.Sprintf("could not open %s's console: %v", strconv.Quote(name), err))
	}
	session := &Session{Terminal: terminal, target: target, principal: principal, opened: s.now(),
		service: s, ended: make(chan struct{}), outputEnded: ReasonConsoleClosed, release: release}
	s.mu.Lock()
	s.sessions[session] = struct{}{}
	s.mu.Unlock()
	go s.watchGuest(context.WithoutCancel(ctx), session, name)
	return session, nil
}

// nmdmDevice is the host's end of a vm-bhyve console: /dev/nmdm-<name>.<n>B.
var nmdmDevice = regexp.MustCompile(`^/dev/nmdm-[A-Za-z0-9][A-Za-z0-9._-]*\.[0-9]+B$`)

// watchGuest ends a VM session once its guest is no longer running.
func (s *Service) watchGuest(ctx context.Context, session *Session, name string) {
	ticker := time.NewTicker(s.watch)
	defer ticker.Stop()
	for {
		select {
		case <-session.ended:
			return
		case <-ticker.C:
			_, running, err := s.guests.Console(ctx, name)
			if err == nil && !running || isNotFound(err) {
				session.end(ReasonGuestStopped)
				return
			}
		}
	}
}

func isNotFound(err error) bool {
	var appErr *common.AppError
	return errors.As(err, &appErr) && appErr.Code == http.StatusNotFound
}

// Finish ends a session for the reason the connection gave, unless something
// had already ended it, and writes its close entry with how long it lasted.
func (s *Service) Finish(ctx context.Context, session *Session, reason string) {
	session.end(reason)
	if session.release != nil {
		session.release()
	}
	s.mu.Lock()
	delete(s.sessions, session)
	reason = session.reason
	s.mu.Unlock()
	s.log(context.WithoutCancel(ctx), audit.Entry{Operation: OperationClose, Target: session.target,
		Principal: session.principal, Success: true, Reason: reason,
		DurationMS: s.now().Sub(session.opened).Milliseconds()})
}

// closeTarget hangs up every console on one jail or guest. The hang-up is
// the terminal's Close, which returns once a shell has gone, so the stop that
// follows does not wait on it.
func (s *Service) closeTarget(target, reason string) {
	s.mu.Lock()
	var open []*Session
	for session := range s.sessions {
		if session.target == target {
			open = append(open, session)
		}
	}
	s.mu.Unlock()
	for _, session := range open {
		session.end(reason)
	}
}

// HangUpJail is CloseJail for a jail about to stop, which is the jails
// service's hook (JD-042).
func (s *Service) HangUpJail(name string) { s.closeTarget("jail/"+name, ReasonJailStopped) }

// HangUpVM closes a guest's console before Jail Deck stops it: the VMs
// service's hook.
func (s *Service) HangUpVM(name string) { s.closeTarget("vm/"+name, ReasonGuestStopped) }

// Open is how many sessions are open, for the tests and nothing else.
func (s *Service) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Service) log(ctx context.Context, entry audit.Entry) {
	entry.Timestamp = s.now()
	if !entry.Success {
		entry.ExitCode = -1
	}
	if err := s.audit.Log(ctx, entry); err != nil {
		common.Logger(ctx, "console").Error("failed to write the audit entry",
			slog.String("operation", entry.Operation), slog.String("target", entry.Target), slog.Any("err", err))
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return err.Error()
}
