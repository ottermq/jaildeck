package vms

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/ottermq/jaildeck/internal/common"
)

const consolePassword = "correct horse battery"

// The adapter is handed a hash the guest's crypt(3) checks the password
// against, and never the password.
func TestCreateHandsTheAdapterAHash(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	req := made
	req.ConsolePassword = consolePassword
	if _, err := s.Create(context.Background(), withName(req, "web")); err != nil {
		t.Fatal(err)
	}
	hash := m.specs[0].PasswordHash
	if !strings.HasPrefix(hash, "$2a$") || strings.Contains(hash, consolePassword) {
		t.Fatalf("hash = %q", hash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(consolePassword)); err != nil {
		t.Errorf("the hash does not check the password: %v", err)
	}
}

func TestNoPasswordIsNoHash(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	if _, err := s.Create(context.Background(), withName(made, "web")); err != nil {
		t.Fatal(err)
	}
	if m.specs[0].PasswordHash != "" {
		t.Errorf("hash = %q", m.specs[0].PasswordHash)
	}
}

// Refused before anything runs, and the refusal never repeats the password.
func TestConsolePasswordsRefused(t *testing.T) {
	for name, password := range map[string]string{
		"short":   "seven77",
		"long":    strings.Repeat("a", 73),
		"newline": "two lines\nof it",
		"tab":     "a\tpassword",
		"utf-8":   "abcdefgh\xff",
	} {
		t.Run(name, func(t *testing.T) {
			m := newMaker()
			s, _ := newMakerService(m)
			req := withName(made, "web")
			req.ConsolePassword = password
			_, err := s.Create(context.Background(), req)
			var appErr *common.AppError
			if !errors.As(err, &appErr) || appErr.Code != http.StatusBadRequest || !strings.Contains(err.Error(), "console_password") {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), password) {
				t.Errorf("the refusal repeats the password: %v", err)
			}
			if len(m.calls) != 0 {
				t.Errorf("ran %v", m.calls)
			}
		})
	}
}

// The longest bcrypt reads, and multi-byte characters counted as bytes.
func TestConsolePasswordsAccepted(t *testing.T) {
	for _, password := range []string{"eight888", strings.Repeat("a", 72), "sí señor, contraseña"} {
		if err := checkConsolePassword(password); err != nil {
			t.Errorf("%q: %v", password, err)
		}
	}
}

// PUT holds the password against the seed's hash and answers only whether
// one is set: same, another, none asked for, none there.
func TestEnsureComparesThePasswordWithoutSayingIt(t *testing.T) {
	m := newMaker()
	s, _ := newMakerService(m)
	withPassword := made
	withPassword.ConsolePassword = consolePassword
	if _, err := s.Ensure(context.Background(), "web", withPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(context.Background(), "plain", made); err != nil {
		t.Fatal(err)
	}
	another := made
	another.ConsolePassword = "another password"
	for _, tc := range []struct {
		name, guest string
		req         CreateRequest
		want        *Difference
	}{
		{"same", "web", withPassword, nil},
		{"another", "web", another, &Difference{Field: "console_password", Want: "set", Have: "another"}},
		{"none asked for", "web", made, &Difference{Field: "console_password", Want: "none", Have: "set"}},
		{"none there", "plain", withPassword, &Difference{Field: "console_password", Want: "set", Have: "none"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ensured, err := s.Ensure(context.Background(), tc.guest, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			var got *Difference
			for _, d := range ensured.Differences {
				if d.Field == "console_password" {
					got = &d
				}
			}
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("difference = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func withName(req CreateRequest, name string) CreateRequest {
	req.Name = name
	return req
}
