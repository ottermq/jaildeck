package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/state"
)

type credentialReaderFunc func() (state.State, error)

func (f credentialReaderFunc) Load() (state.State, error) { return f() }

func TestAuthServiceLogin(t *testing.T) {
	hash, err := Hash("correct password")
	if err != nil {
		t.Fatal(err)
	}
	loadErr := errors.New("load failed")
	for _, tt := range []struct {
		name, password, hash string
		loadErr, wantErr     error
	}{
		{name: "success", password: "correct password", hash: hash},
		{name: "wrong password", password: "wrong", hash: hash, wantErr: ErrInvalidCredential},
		{name: "load failure", loadErr: loadErr, wantErr: loadErr},
		{name: "malformed hash", hash: "invalid", wantErr: ErrMalformed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sessions := NewSessionStore()
			service := NewAuthService(credentialReaderFunc(func() (state.State, error) {
				return state.State{PasswordHash: tt.hash}, tt.loadErr
			}), sessions)
			token, err := service.Login(context.Background(), tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Login() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if token != "" || len(sessions.sessions) != 0 {
					t.Fatal("failed login created a session or returned a token")
				}
			} else {
				if token == "" || len(sessions.sessions) != 1 {
					t.Fatal("successful login must create one session and return its token")
				}
				got, err := service.Authenticate(context.Background(), token)
				if err != nil || got.CredentialFingerprint != sha256.Sum256([]byte(hash)) {
					t.Fatalf("Authenticate() = %+v, %v; want matching credential fingerprint", got, err)
				}
			}
			if len(service.loginQueue) != 0 || len(service.loginSem) != 0 {
				t.Fatal("Login() did not release its queue and verification slots")
			}
		})
	}
}

func TestAuthServiceAuthenticate(t *testing.T) {
	const originalHash = "original credential"
	loadErr := errors.New("load failed")

	// env is the state a case may tamper with between creating a valid session
	// and calling Authenticate.
	type env struct {
		service   *AuthService
		sessions  *SessionStore
		token     string
		now       time.Time
		hash      string
		readerErr error
	}

	tests := []struct {
		name        string
		setup       func(e *env)
		wantErr     error
		wantRevoked bool // the session must be gone from the store afterwards
	}{
		{
			name: "valid",
		},
		{
			name:    "missing",
			setup:   func(e *env) { e.token = "unknown" },
			wantErr: ErrUnauthenticated,
		},
		{
			name:        "expired",
			setup:       func(e *env) { e.now = e.now.Add(e.sessions.MaxAge()) },
			wantErr:     ErrUnauthenticated,
			wantRevoked: true,
		},
		{
			name:        "credential changed",
			setup:       func(e *env) { e.hash = "new credential" },
			wantErr:     ErrUnauthenticated,
			wantRevoked: true,
		},
		{
			name:        "logout",
			setup:       func(e *env) { e.service.Logout(context.Background(), e.token) },
			wantErr:     ErrUnauthenticated,
			wantRevoked: true,
		},
		{
			// A transient read failure must not log the operator out.
			name:    "load failure",
			setup:   func(e *env) { e.readerErr = loadErr },
			wantErr: loadErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &env{
				sessions: NewSessionStore(),
				now:      time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				hash:     originalHash,
			}
			e.sessions.now = func() time.Time { return e.now }
			e.service = NewAuthService(credentialReaderFunc(func() (state.State, error) {
				return state.State{PasswordHash: e.hash}, e.readerErr
			}), e.sessions)

			fingerprint := sha256.Sum256([]byte(originalHash))
			token, err := e.sessions.Create(fingerprint)
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			e.token = token
			want := Session{CredentialFingerprint: fingerprint, ExpiresAt: e.now.Add(e.sessions.MaxAge())}

			if tt.setup != nil {
				tt.setup(e)
			}

			got, err := e.service.Authenticate(context.Background(), e.token)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				want = Session{}
			}
			if got != want {
				t.Fatalf("Authenticate() session = %+v, want %+v", got, want)
			}

			// Every case pins what happened to the session that was created,
			// not only the revoking ones: Authenticate must not evict a session
			// it merely failed to read.
			_, storeErr := e.sessions.Get(token)
			if tt.wantRevoked {
				if !errors.Is(storeErr, ErrTokenNotFound) {
					t.Errorf("Get() after revocation = %v, want ErrTokenNotFound", storeErr)
				}
			} else if storeErr != nil {
				t.Errorf("Get() = %v, want the session to survive", storeErr)
			}
		})
	}
}

func TestAuthServiceLoginQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		loadErr := errors.New("load failed")
		calls := 0
		service := NewAuthService(credentialReaderFunc(func() (state.State, error) {
			calls++
			<-release
			return state.State{}, loadErr
		}), NewSessionStore())
		results := make(chan error, maxLoginQueue)
		login := func(ctx context.Context) {
			token, err := service.Login(ctx, "password")
			if token != "" {
				t.Error("failed login returned a token")
			}
			results <- err
		}
		go login(context.Background())
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		for range maxLoginQueue - 1 {
			go login(ctx)
		}
		synctest.Wait()
		if calls != 1 || len(service.loginQueue) != maxLoginQueue {
			t.Fatalf("calls = %d, queued = %d; want 1, %d", calls, len(service.loginQueue), maxLoginQueue)
		}
		token, err := service.Login(context.Background(), "password")
		if token != "" || !errors.Is(err, ErrMaxRequestConcurrencyReached) {
			t.Fatalf("full queue Login() = %q, %v", token, err)
		}
		cancel()
		synctest.Wait()
		for range maxLoginQueue - 1 {
			if err := <-results; !errors.Is(err, context.Canceled) {
				t.Errorf("waiting login error = %v, want context.Canceled", err)
			}
		}
		if len(service.loginQueue) != 1 || calls != 1 {
			t.Fatal("canceled waiters did not release slots or reached credential verification")
		}
		close(release)
		synctest.Wait()
		if err := <-results; !errors.Is(err, loadErr) {
			t.Fatalf("active login error = %v, want %v", err, loadErr)
		}
		if len(service.loginQueue) != 0 || len(service.loginSem) != 0 {
			t.Fatal("login slots leaked")
		}
		if _, err := service.Login(context.Background(), "password"); !errors.Is(err, loadErr) {
			t.Fatalf("login after queue drained: %v", err)
		}
		if calls != 2 || len(service.sessions.sessions) != 0 {
			t.Fatal("subsequent login did not verify or failed logins created sessions")
		}
	})
}

func TestAuthServiceSessionMaxAge(t *testing.T) {
	sessions := NewSessionStore()
	sessions.maxAge = 42 * time.Minute
	service := NewAuthService(nil, sessions)
	if got := service.SessionMaxAge(); got != sessions.maxAge {
		t.Fatalf("SessionMaxAge() = %v, want %v", got, sessions.maxAge)
	}
}
