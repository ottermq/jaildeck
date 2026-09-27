package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const defaultSessionMaxAge = 12 * time.Hour

var (
	ErrTokenNotFound  = errors.New("token not found")
	ErrSessionExpired = errors.New("session expired")
)

type Session struct {
	CredentialFingerprint [32]byte
	ExpiresAt             time.Time
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]Session
	maxAge   time.Duration
	now      func() time.Time
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]Session),
		maxAge:   defaultSessionMaxAge,
		now: func() time.Time {
			return time.Now()
		},
	}
}

func (s *SessionStore) MaxAge() time.Duration {
	return s.maxAge
}

func (s *SessionStore) Create(fingerprint [32]byte) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	session := Session{
		CredentialFingerprint: fingerprint,
		ExpiresAt:             s.now().Add(s.maxAge),
	}
	s.mu.Lock()
	s.sessions[token] = session
	s.mu.Unlock()
	return token, nil
}

// Get retrieves a session by its token. If the session does not exist or has expired, it returns an error.
func (s *SessionStore) Get(token string) (Session, error) {
	s.mu.Lock()
	session, ok := s.sessions[token]
	now := s.now()
	s.mu.Unlock()
	if !ok {
		return Session{}, ErrTokenNotFound
	}

	// If session now < ExpiresAt -> valid
	// "NOT" here is less readable, but avoids dealing with "Now >= ExpiresAt"
	if !now.Before(session.ExpiresAt) {
		s.Delete(token)
		return Session{}, ErrSessionExpired
	}

	return session, nil
}

func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func generateToken() (string, error) {
	b := make([]byte, 32)

	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
