package auth

import (
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	store := NewSessionStore()
	if got := store.MaxAge(); got != 12*time.Hour {
		t.Fatalf("MaxAge() = %v, want 12h", got)
	}
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }
	fingerprint := [32]byte{1, 2, 3}
	want := Session{CredentialFingerprint: fingerprint, ExpiresAt: base.Add(12 * time.Hour)}
	var previous string
	for range 2 {
		token, err := store.Create(fingerprint)
		if err != nil {
			t.Fatalf("Create() failed: %v", err)
		}
		if token == previous {
			t.Fatal("Create() returned the same token twice")
		}
		previous = token
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatalf("decode token: %v", err)
		}
		if len(decoded) != 32 {
			t.Errorf("decoded token has %d bytes, want 32", len(decoded))
		}
		for range 2 {
			got, err := store.Get(token)
			if err != nil || got != want {
				t.Fatalf("Get() = %+v, %v; want %+v, nil", got, err, want)
			}
		}
	}
}

func TestSessionStore_GetMissing(t *testing.T) {
	store := NewSessionStore()
	for _, token := range []string{"", "unknown"} {
		got, err := store.Get(token)
		if !errors.Is(err, ErrTokenNotFound) || got != (Session{}) {
			t.Errorf("Get(%q) = %+v, %v; want zero session, ErrTokenNotFound", token, got, err)
		}
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	for _, tt := range []struct {
		name    string
		offset  time.Duration
		expired bool
	}{
		{name: "just before expiry", offset: -time.Nanosecond},
		{name: "exactly at expiry", expired: true},
		{name: "after expiry", offset: time.Hour, expired: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := NewSessionStore()
			base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
			store.now = func() time.Time { return base }
			fingerprint := [32]byte{42}
			token, err := store.Create(fingerprint)
			if err != nil {
				t.Fatalf("Create() failed: %v", err)
			}
			expiresAt := base.Add(store.MaxAge())
			store.now = func() time.Time { return expiresAt.Add(tt.offset) }
			got, err := store.Get(token)
			if !tt.expired {
				want := Session{CredentialFingerprint: fingerprint, ExpiresAt: expiresAt}
				if err != nil || got != want {
					t.Fatalf("Get() = %+v, %v; want %+v, nil", got, err, want)
				}
				return
			}
			if !errors.Is(err, ErrSessionExpired) || got != (Session{}) {
				t.Fatalf("Get() = %+v, %v; want zero session, ErrSessionExpired", got, err)
			}
			if _, err := store.Get(token); !errors.Is(err, ErrTokenNotFound) {
				t.Fatalf("Get() after expiry = %v, want ErrTokenNotFound", err)
			}
		})
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewSessionStore()
	token, err := store.Create([32]byte{1})
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	other, err := store.Create([32]byte{2})
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	store.Delete(token)
	store.Delete(token)
	store.Delete("unknown")
	if got, err := store.Get(token); !errors.Is(err, ErrTokenNotFound) || got != (Session{}) {
		t.Fatalf("Get() after deletion = %+v, %v; want zero session, ErrTokenNotFound", got, err)
	}
	if got, err := store.Get(other); err != nil || got.CredentialFingerprint != ([32]byte{2}) {
		t.Fatalf("Get() for other session = %+v, %v; want original fingerprint, nil", got, err)
	}
}

func TestSessionStore_ConcurrentCreateAndGet(t *testing.T) {
	store := NewSessionStore()
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 32 {
		wg.Go(func() {
			<-start
			fingerprint := [32]byte{byte(i)}
			want := Session{CredentialFingerprint: fingerprint, ExpiresAt: base.Add(12 * time.Hour)}
			for range 16 {
				token, err := store.Create(fingerprint)
				if err != nil {
					t.Errorf("Create() failed: %v", err)
					return
				}
				for range 2 {
					got, err := store.Get(token)
					if err != nil || got != want {
						t.Errorf("Get() = %+v, %v; want %+v, nil", got, err, want)
						return
					}
				}
				store.Delete(token)
			}
		})
	}
	close(start)
	wg.Wait()
}
