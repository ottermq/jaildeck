package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/state"
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

	t.Run("wrong password", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Login(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"wrong"}`)))
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
	h.Login(rec, httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"password":"correct password"}`)))
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
	h.Me(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want 200", rec.Code)
	}
	var me MeResponse
	if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(service.SessionMaxAge()); !me.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", me.ExpiresAt, want)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.Logout(rec, req)
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
	h.Me(rec, req)
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

func TestAuthHandlerMeUnauthorized(t *testing.T) {
	h := NewAuthHandler(NewAuthService(nil, NewSessionStore()))
	for _, token := range []string{"", "unknown"} {
		t.Run("token="+token, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
			if token != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			}
			rec := httptest.NewRecorder()
			h.Me(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
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
