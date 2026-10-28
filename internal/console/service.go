package console

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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
)

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
	audit  audit.Logger
	now    func() time.Time

	mu       sync.Mutex
	sessions map[*Session]struct{}
}

func NewService(shells Shells, jails JailState, auditLogger audit.Logger) *Service {
	return &Service{shells: shells, jails: jails, audit: auditLogger, now: time.Now,
		sessions: map[*Session]struct{}{}}
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
}

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
		opened: s.now(), service: s, ended: make(chan struct{})}
	s.mu.Lock()
	s.sessions[session] = struct{}{}
	s.mu.Unlock()
	return session, nil
}

// Finish ends a session for the reason the connection gave, unless something
// had already ended it, and writes its close entry with how long it lasted.
func (s *Service) Finish(ctx context.Context, session *Session, reason string) {
	session.end(reason)
	s.mu.Lock()
	delete(s.sessions, session)
	reason = session.reason
	s.mu.Unlock()
	s.log(context.WithoutCancel(ctx), audit.Entry{Operation: OperationClose, Target: session.target,
		Principal: session.principal, Success: true, Reason: reason,
		DurationMS: s.now().Sub(session.opened).Milliseconds()})
}

// CloseJail hangs up every console in a jail, before it stops. The hang-up
// is the terminal's Close, which returns once the shell has gone, so the
// stop that follows does not wait on it.
func (s *Service) CloseJail(name, reason string) {
	target := "jail/" + name
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
func (s *Service) HangUpJail(name string) { s.CloseJail(name, ReasonJailStopped) }

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
