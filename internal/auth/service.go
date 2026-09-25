package auth

import (
	"context"
	"crypto/sha256"
	"net/http"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/common"
	"codeberg.org/Otter-Labs/jaildeck/internal/state"
)

const maxLoginQueue = 8

var (
	ErrInvalidCredential            = common.NewAppError(http.StatusUnauthorized, "invalid credentials")
	ErrMaxRequestConcurrencyReached = common.NewAppError(http.StatusTooManyRequests, "too many requests are already queued")
	ErrUnauthenticated              = common.NewAppError(http.StatusUnauthorized, "unauthenticated")
)

type CredentialReader interface {
	Load() (state.State, error)
}

type AuthService struct {
	state      CredentialReader
	sessions   *SessionStore
	loginQueue chan struct{}
	loginSem   chan struct{}
}

func NewAuthService(stateStore CredentialReader, sessions *SessionStore) *AuthService {
	return &AuthService{
		state:      stateStore,
		sessions:   sessions,
		loginQueue: make(chan struct{}, maxLoginQueue),
		loginSem:   make(chan struct{}, 1),
	}
}

// Login verifies password against the stored credential and returns a new
// session token. It returns ErrInvalidCredential for a wrong password,
// ErrMaxRequestConcurrencyReached when too many (1 veryfing + N-1 waiting)
// logins are already in flight, and ctx.Err() if ctx is cancelled while waiting.
func (s *AuthService) Login(ctx context.Context, password string) (string, error) {
	// Shed load rather than accumulate blocked goroutines: nothing else times
	// a blocked handler out, since WriteTimeout is unset (JD-014).
	select {
	case s.loginQueue <- struct{}{}:
		defer func() { <-s.loginQueue }()
	default:
		return "", ErrMaxRequestConcurrencyReached
	}

	// One verification at a time: argon2id at 64 MiB (JD-003) allocates that
	// much per call, so concurrent verifications multiply it. This bounds
	// memory, not attempt rate.
	select {
	case s.loginSem <- struct{}{}:
		defer func() { <-s.loginSem }()
		return s.login(password)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *AuthService) login(password string) (string, error) {
	st, err := s.state.Load()
	if err != nil {
		return "", err // falls on Status Code 500
	}

	ok, err := Verify(password, st.PasswordHash)
	if err != nil {
		return "", err // falls on Status Code 500
	}
	if !ok {
		return "", ErrInvalidCredential
	}

	return s.sessions.Create(credentialFingerprint(st.PasswordHash))
}

func (s *AuthService) Logout(ctx context.Context, token string) {
	s.sessions.Delete(token)
}

func (s *AuthService) Authenticate(ctx context.Context, token string) (Session, error) {
	session, err := s.sessions.Get(token)
	if err != nil {
		// ErrTokenNotFound and ErrSessionExpired messages are swallowed on purpose
		return Session{}, ErrUnauthenticated
	}
	st, err := s.state.Load()
	if err != nil {
		return Session{}, err // falls on Status Code 500
	}
	currentFingerprint := credentialFingerprint(st.PasswordHash)
	if session.CredentialFingerprint != currentFingerprint {
		s.sessions.Delete(token)
		// same situation: we are not sending "credential changed" for security's sake.
		return Session{}, ErrUnauthenticated
	}
	return session, nil
}

func (s *AuthService) SessionMaxAge() time.Duration {
	return s.sessions.MaxAge()
}

func credentialFingerprint(passwordHash string) [32]byte {
	return sha256.Sum256([]byte(passwordHash))
}
