package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/state"
)

func TestRunPasswd_Stdin(t *testing.T) {
	for _, tt := range []struct {
		name, input, password string
	}{
		{"newline", "abcdef\n", "abcdef"},
		{"CRLF", "abcdef\r\n", "abcdef"},
		{"trailing space", "abcdef \n", "abcdef "},
		{"no newline", "abcdef", "abcdef"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var out bytes.Buffer
			if err := runPasswd([]string{"jaildeck", "passwd", "--stdin"}, strings.NewReader(tt.input), &out, dir); err != nil {
				t.Fatalf("runPasswd() = %v", err)
			}
			st, err := state.NewStore(dir).Load()
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			if ok, err := auth.Verify(tt.password, st.PasswordHash); err != nil || !ok {
				t.Errorf("Verify() = %v, %v, want true, nil", ok, err)
			}
			if got := out.String(); got != "password successfully stored\n" {
				t.Errorf("output = %q, want success message", got)
			}
		})
	}
}

func TestRunPasswd_InvalidPassword(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		want        error
	}{
		{"empty EOF", "", ErrEmptyPassword},
		{"empty line", "\n", ErrEmptyPassword},
		{"too short", "abcde\n", ErrShortPassword},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := runPasswd([]string{"jaildeck", "passwd", "--stdin"}, strings.NewReader(tt.input), io.Discard, dir)
			if !errors.Is(err, tt.want) {
				t.Errorf("runPasswd() = %v, want %v", err, tt.want)
			}
			if _, err := state.NewStore(dir).Load(); !errors.Is(err, state.ErrNotConfigured) {
				t.Errorf("Load() = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestRunPasswd_UnexpectedArguments(t *testing.T) {
	err := runPasswd(
		[]string{"jaildeck", "passwd", "--stdin", "extra"},
		strings.NewReader("abcdef\n"),
		io.Discard,
		t.TempDir(),
	)

	if !errors.Is(err, ErrUnexpectedArgs) {
		t.Fatalf("runPasswd() error = %v, want ErrUnexpectedArgs", err)
	}
}

func TestRunPasswd_PermissionHint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(parent, 0o700); err != nil {
			t.Error(err)
		}
	})
	var out bytes.Buffer
	err := runPasswd([]string{"jaildeck", "passwd", "--stdin"}, strings.NewReader("abcdef\n"), &out, filepath.Join(parent, "state"))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("runPasswd() = %v, want wrapped permission error", err)
	}
	if !strings.Contains(err.Error(), "(run as root, or set JAILDECK_STATE_DIR to a writable path)") {
		t.Errorf("error = %q, want permission hint", err)
	}
	if out.Len() != 0 {
		t.Errorf("output = %q, want no success message", out.String())
	}
}

func TestRunPasswd_ReplacesCredentialAndPreservesTokens(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(dir)
	tokens := []state.Token{{Name: "first-token"}, {Name: "second-token"}}
	if err := store.Save(state.State{APITokens: tokens}); err != nil {
		t.Fatalf("seeding state: %v", err)
	}
	for _, password := range []string{"original-password", "replacement-password"} {
		if err := runPasswd([]string{"jaildeck", "passwd", "--stdin"}, strings.NewReader(password+"\n"), io.Discard, dir); err != nil {
			t.Fatalf("runPasswd() = %v", err)
		}
		st, err := store.Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if ok, err := auth.Verify(password, st.PasswordHash); err != nil || !ok {
			t.Errorf("Verify(current password) = %v, %v, want true, nil", ok, err)
		}
		if !reflect.DeepEqual(st.APITokens, tokens) {
			t.Errorf("APITokens = %v, want %v", st.APITokens, tokens)
		}
		if password == "replacement-password" {
			if ok, err := auth.Verify("original-password", st.PasswordHash); err != nil || ok {
				t.Errorf("Verify(old password) = %v, %v, want false, nil", ok, err)
			}
		}
	}
}

func TestRunPasswd_HelpIsNotAnError(t *testing.T) {
	var out bytes.Buffer
	err := runPasswd([]string{"jaildeck", "passwd", "-h"}, strings.NewReader(""), &out, t.TempDir())
	if err != nil {
		t.Errorf("runPasswd(-h) = %v, want nil — help is not a failure", err)
	}
	if !strings.Contains(out.String(), "-stdin") {
		t.Errorf("usage = %q, want it to mention -stdin", out.String())
	}
}

// TestWithPermissionHint covers the branch directly: reaching a non-permission
// failure through runPasswd is impractical, because Load intercepts almost every
// malformed path before Save is called. TestRunPasswd_PermissionHint remains the
// check that the hint reaches the operator through the real call path.
func TestWithPermissionHint(t *testing.T) {
	const hint = "(run as root, or set JAILDECK_STATE_DIR to a writable path)"

	for _, tt := range []struct {
		name     string
		err      error
		wantHint bool
		wantIs   error
	}{
		{
			name:     "permission error gains the hint",
			err:      fmt.Errorf("creating state dir: %w", os.ErrPermission),
			wantHint: true,
			wantIs:   os.ErrPermission,
		},
		{
			name:     "other errors are returned unchanged",
			err:      fmt.Errorf("creating state dir: %w", os.ErrNotExist),
			wantHint: false,
			wantIs:   os.ErrNotExist,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := withPermissionHint(tt.err)

			if gotHint := strings.Contains(got.Error(), hint); gotHint != tt.wantHint {
				t.Errorf("hint present = %v, want %v; error = %q", gotHint, tt.wantHint, got)
			}
			// The hint must wrap, never replace: callers still branch on the cause.
			if !errors.Is(got, tt.wantIs) {
				t.Errorf("errors.Is(got, %v) = false, want true; error = %q", tt.wantIs, got)
			}
		})
	}
}
