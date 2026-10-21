package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/state"
)

func TestAuthHandlerSessionLifecycle(t *testing.T) {
	hash, err := Hash("correct password")
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewSessionStore()
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	sessions.now = func() time.Time { return now }
	service := NewAuthService(credentialReaderFunc(func() (state.State, error) {
		return state.State{PasswordHash: hash}, nil
	}), sessions)
	h := NewAuthHandler(service)
	router := chi.NewRouter()
	RegisterAuthRoutes(router, h)

	t.Run("wrong password", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"wrong"}`)))
		resp := rec.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if len(resp.Cookies()) != 0 || len(resp.Header.Values("Set-Cookie")) != 0 {
			t.Fatal("failed login set a cookie")
		}
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"correct password"}`)))
	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var login LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	if !login.Authenticated {
		t.Error("login did not report authenticated")
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Path != "/" || cookie.Value == "" {
		t.Errorf("unexpected session cookie: %+v", cookie)
	}
	if !cookie.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if !cookie.Secure {
		t.Error("session cookie is not Secure")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", cookie.SameSite)
	}
	if want := int(service.SessionMaxAge().Seconds()); cookie.MaxAge != want {
		t.Errorf("MaxAge = %d, want %d", cookie.MaxAge, want)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
	var me MeResponse
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(service.SessionMaxAge()); !me.Principal.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", me.Principal.ExpiresAt, want)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	resp = rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("logout status = %d, body = %q; want 204 and empty body", resp.StatusCode, rec.Body.String())
	}
	cookies = resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("logout cookies = %d, want 1", len(cookies))
	}
	if got := cookies[0]; got.MaxAge >= 0 || got.Name != cookie.Name || got.Path != cookie.Path || got.Value != "" {
		t.Errorf("logout did not clear the session cookie: %+v", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("me after logout status = %d, want 401", rec.Code)
	}
}

func TestAuthHandlerLoginInvalidBody(t *testing.T) {
	h := NewAuthHandler(NewAuthService(nil, NewSessionStore()))
	for _, tt := range []struct {
		name, body string
		wantStatus int
	}{
		{"empty", "", http.StatusBadRequest},
		{"malformed", `{"password":`, http.StatusBadRequest},
		{"wrong type", `{"password":123}`, http.StatusBadRequest},
		{"oversized", `{"password":"` + strings.Repeat("a", maxLoginBytes) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.Login(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(tt.body)))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if len(rec.Header().Values("Set-Cookie")) != 0 {
				t.Error("invalid request set a cookie")
			}
		})
	}
}

func TestRequireAuth(t *testing.T) {
	sessions := NewSessionStore()
	const hash = "stored password hash"
	const apiToken = "jd_test_token"
	const tokenName = "test token"
	h := NewAuthHandler(NewAuthService(credentialReaderFunc(func() (state.State, error) {
		return state.State{
			PasswordHash: hash,
			APITokens:    []state.Token{{Name: tokenName, Hash: HashToken(apiToken)}},
		}, nil
	}), sessions))
	token, err := sessions.Create(credentialFingerprint(hash))
	if err != nil {
		t.Fatal(err)
	}
	wantSession, err := sessions.Get(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, token, authorization string
		wantStatus                 int
		wantNext                   bool
		wantTokenName              string
		wantKind                   PrincipalKind
	}{
		{"no cookie", "", "", http.StatusUnauthorized, false, "", ""},
		{"unknown token", "unknown", "", http.StatusUnauthorized, false, "", ""},
		{"valid token", token, "", http.StatusNoContent, true, "", kindSession},
		{"valid bearer token", "", "Bearer " + apiToken, http.StatusNoContent, true, tokenName, kindToken},
		{"lowercase bearer scheme", "", "bearer " + apiToken, http.StatusNoContent, true, tokenName, kindToken},
		{"unknown bearer token", "", "Bearer unknown", http.StatusUnauthorized, false, "", ""},
		{"basic with valid cookie", token, "Basic xyz", http.StatusNoContent, true, "", kindSession},
		{"bad bearer with valid cookie", token, "Bearer unknown", http.StatusUnauthorized, false, "", ""},
		{"bearer without token with valid cookie", token, "Bearer", http.StatusNoContent, true, "", kindSession},
		{"blank bearer with valid cookie", token, "Bearer    ", http.StatusNoContent, true, "", kindSession},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			if tt.token != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tt.token})
			}
			rec := httptest.NewRecorder()
			called := false
			var logs bytes.Buffer
			req = req.WithContext(common.ContextWithLogger(req.Context(), slog.New(slog.NewJSONHandler(&logs, nil))))
			h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if got, ok := TokenNameFromContext(r.Context()); got != tt.wantTokenName || ok != (tt.wantTokenName != "") {
					t.Errorf("context token name = %q, present = %v; want %q", got, ok, tt.wantTokenName)
				}
				if got, ok := SessionFromContext(r.Context()); tt.wantTokenName != "" {
					if ok {
						t.Errorf("bearer authentication set a session in context: %+v", got)
					}
				} else if !ok || got != wantSession {
					t.Errorf("context session = %+v, present = %v; want %+v", got, ok, wantSession)
				}
				common.LoggerFromContext(r.Context()).Info("handler")
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(rec, req)
			if called != tt.wantNext {
				t.Errorf("next called = %v, want %v", called, tt.wantNext)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if len(rec.Header().Values("Set-Cookie")) != 0 {
				t.Errorf("authentication set a cookie: %q", rec.Header().Values("Set-Cookie"))
			}

			// Gated on wantNext, not on wantKind: every request that reaches a handler must
			// name its credential kind, so a row that forgets to set one fails here rather
			// than silently skipping the assertion.
			if tt.wantNext {
				var record map[string]any
				if err := json.NewDecoder(&logs).Decode(&record); err != nil {
					t.Fatalf("decode handler record: %v; logs: %s", err, logs.String())
				}
				if got := record["principal_kind"]; got != string(tt.wantKind) {
					t.Errorf("principal_kind = %v, want %q", got, tt.wantKind)
				}
			}
		})
	}
}

func TestAuthHandlerMe(t *testing.T) {
	h := NewAuthHandler(nil)
	session := Session{ExpiresAt: time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)}
	for _, tt := range []struct {
		name       string
		ctx        context.Context
		wantStatus int
	}{
		{"session in context", context.WithValue(context.Background(), sessionKey{}, session), http.StatusOK},
		{"bare context", context.Background(), http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.Me(rec, httptest.NewRequest(http.MethodGet, "/auth/me", nil).WithContext(tt.ctx))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus != http.StatusOK {
				if strings.Contains(rec.Body.String(), `"expires_at"`) {
					t.Error("unauthorized response includes session data")
				}
				return
			}
			var me MeResponse
			if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
				t.Fatal(err)
			}
			if !me.Principal.ExpiresAt.Equal(session.ExpiresAt) {
				t.Errorf("ExpiresAt = %v, want %v", me.Principal.ExpiresAt, session.ExpiresAt)
			}
		})
	}
}

func TestAuthHandlerLogoutWithoutCookie(t *testing.T) {
	h := NewAuthHandler(NewAuthService(nil, NewSessionStore()))
	rec := httptest.NewRecorder()
	h.Logout(rec, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))
	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d, body = %q; want 204 and empty body", resp.StatusCode, rec.Body.String())
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if got := cookies[0]; got.Name != sessionCookieName || got.Path != "/" || got.MaxAge >= 0 {
		t.Errorf("unexpected deletion cookie: %+v", got)
	}
}

func TestWithPrincipalLogged(t *testing.T) {
	for _, tt := range []struct {
		kind PrincipalKind
		name string
	}{
		{kindSession, ""},
		{kindToken, "automation"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			entry := &principalTestLogEntry{logger: logger}
			handler := middleware.RequestLogger(entry)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = withPrincipalLogged(r, tt.kind, tt.name)
				common.LoggerFromContext(r.Context()).Info("handler")
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(common.ContextWithLogger(req.Context(), logger))
			handler.ServeHTTP(httptest.NewRecorder(), req)

			decoder := json.NewDecoder(&logs)
			for _, message := range []string{"handler", "request"} {
				var record map[string]any
				if err := decoder.Decode(&record); err != nil {
					t.Fatalf("decode %s record: %v", message, err)
				}
				if got := record["msg"]; got != message {
					t.Errorf("message = %v, want %q", got, message)
				}
				if got := record["principal_kind"]; got != string(tt.kind) {
					t.Errorf("%s principal_kind = %v, want %q", message, got, tt.kind)
				}
				if tt.name != "" {
					if got := record["principal"]; got != tt.name {
						t.Errorf("%s principal = %v, want %q", message, got, tt.name)
					}
				} else if _, ok := record["principal"]; ok {
					t.Errorf("%s session record includes principal", message)
				}
			}
			var extra map[string]any
			if err := decoder.Decode(&extra); err != io.EOF {
				t.Fatalf("expected exactly two records, got %v, error %v", extra, err)
			}
		})
	}
}

type principalTestLogEntry struct {
	logger *slog.Logger
}

func (e *principalTestLogEntry) NewLogEntry(*http.Request) middleware.LogEntry {
	return e
}

func (e *principalTestLogEntry) AddAttrs(args ...any) {
	e.logger = e.logger.With(args...)
}

func (e *principalTestLogEntry) Write(int, int, http.Header, time.Duration, any) {
	e.logger.Info("request")
}

func (e *principalTestLogEntry) Panic(any, []byte) {
	e.logger.Error("panic")
}
