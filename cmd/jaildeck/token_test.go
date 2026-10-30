package main

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/state"
)

func TestRunToken_AddAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(dir)
	initial := state.State{PasswordHash: "existing-password-hash", APITokens: []state.Token{{Name: "existing", Hash: auth.HashToken("existing-token")}}}
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := runToken([]string{"add", "backup"}, &out, &errOut, dir); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.PasswordHash != initial.PasswordHash {
		t.Errorf("PasswordHash = %q, want %q", st.PasswordHash, initial.PasswordHash)
	}
	if len(st.APITokens) != 2 {
		t.Fatalf("token count = %d, want 2", len(st.APITokens))
	}
	if !reflect.DeepEqual(st.APITokens[0], initial.APITokens[0]) {
		t.Error("add changed the existing token")
	}
	plain := strings.TrimSpace(out.String())
	if !strings.HasPrefix(plain, "jd_") || len(strings.Fields(out.String())) != 1 {
		t.Errorf("stdout = %q, want a single plaintext token", out.String())
	}
	token := st.APITokens[1]
	if token.Name != "backup" || token.Hash != auth.HashToken(plain) || token.CreatedAt.IsZero() {
		t.Errorf("unexpected stored token: %+v", token)
	}
	out.Reset()
	errOut.Reset()
	if err := runToken([]string{"add", "backup"}, &out, &errOut, dir); !errors.Is(err, auth.ErrTokenNameAlreadyExists) {
		t.Errorf("duplicate add = %v, want ErrTokenNameAlreadyExists", err)
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, st) {
		t.Error("duplicate add changed state")
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Error("duplicate add printed output")
	}
}

func TestRunToken_Revoke(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(dir)
	keep := state.Token{Name: "keep", Hash: auth.HashToken("keep-token")}
	if err := store.Save(state.State{PasswordHash: "password-hash", APITokens: []state.Token{{Name: "backup", Hash: auth.HashToken("backup-token")}, keep}}); err != nil {
		t.Fatal(err)
	}
	want := state.State{PasswordHash: "password-hash", APITokens: []state.Token{keep}}
	for attempt := 0; attempt < 2; attempt++ {
		var out bytes.Buffer
		err := runToken([]string{"revoke", "backup"}, &out, io.Discard, dir)
		if attempt == 0 && err != nil {
			t.Fatal(err)
		}
		if attempt == 1 {
			if !errors.Is(err, auth.ErrUnknownToken) {
				t.Errorf("second revoke = %v, want ErrUnknownToken", err)
			}
			if out.Len() != 0 {
				t.Errorf("failed revoke output = %q", out.String())
			}
		}
		got, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("state after revoke = %+v, want %+v", got, want)
		}
	}
}

func TestRunToken_List(t *testing.T) {
	dir := t.TempDir()
	tokens := []state.Token{
		{Name: "backup", Hash: auth.HashToken("first-secret"), CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{Name: "deploy", Hash: auth.HashToken("second-secret"), CreatedAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)},
	}
	if err := state.NewStore(dir).Save(state.State{APITokens: tokens}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := runToken([]string{"list"}, &out, &errOut, dir); err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if !strings.Contains(out.String(), token.Name) || !strings.Contains(out.String(), token.CreatedAt.Format(time.RFC3339)) {
			t.Errorf("list output = %q, missing name or creation time for %q", out.String(), token.Name)
		}
		if strings.Contains(out.String()+errOut.String(), token.Hash) {
			t.Errorf("list exposed hash for %q", token.Name)
		}
	}
}

func TestRunToken_InvalidArguments(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		want    error
		message string
	}{
		{name: "missing verb", message: "expected 'add', 'list' or 'revoke' command"},
		{name: "unknown verb", args: []string{"unknown"}, message: "unknown command:"},
		{name: "add missing name", args: []string{"add"}, message: "usage:"},
		{name: "revoke missing name", args: []string{"revoke"}, message: "usage:"},
		{name: "add extra", args: []string{"add", "backup", "extra"}, want: ErrUnexpectedArgs},
		{name: "add unknown scope", args: []string{"add", "backup", "--scope", "root"}, message: "is not a scope"},
		{name: "add scope without a value", args: []string{"add", "backup", "--scope"}, message: "usage:"},
		{name: "add unknown flag", args: []string{"add", "backup", "--all"}, message: "unknown flag"},
		{name: "list extra", args: []string{"list", "extra"}, want: ErrUnexpectedArgs},
		{name: "revoke extra", args: []string{"revoke", "backup", "extra"}, want: ErrUnexpectedArgs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			var out, errOut bytes.Buffer
			err := runToken(tt.args, &out, &errOut, dir)
			if err == nil {
				t.Fatal("expected argument error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
			if tt.message != "" && !strings.Contains(err.Error(), tt.message) {
				t.Errorf("error = %q, want substring %q", err, tt.message)
			}
			if out.Len() != 0 || errOut.Len() != 0 {
				t.Error("invalid arguments printed output")
			}
			if _, err := state.NewStore(dir).Load(); !errors.Is(err, state.ErrNotConfigured) {
				t.Errorf("Load() = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestRunToken_FreshInstall(t *testing.T) {
	for _, verb := range []string{"add", "list", "revoke"} {
		t.Run(verb, func(t *testing.T) {
			dir := t.TempDir()
			store := state.NewStore(dir)
			if _, err := store.Load(); !errors.Is(err, state.ErrNotConfigured) {
				t.Fatalf("initial Load() = %v, want ErrNotConfigured", err)
			}
			args := []string{verb}
			if verb != "list" {
				args = append(args, "backup")
			}
			var out bytes.Buffer
			err := runToken(args, &out, io.Discard, dir)
			if verb == "revoke" {
				if !errors.Is(err, auth.ErrUnknownToken) {
					t.Errorf("revoke = %v, want ErrUnknownToken", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			st, err := store.Load()
			if verb == "add" {
				if err != nil {
					t.Fatal(err)
				}
				if len(st.APITokens) != 1 || st.APITokens[0].Name != "backup" || st.APITokens[0].Hash != auth.HashToken(strings.TrimSpace(out.String())) {
					t.Errorf("unexpected persisted state: %+v", st)
				}
			} else if !errors.Is(err, state.ErrNotConfigured) {
				t.Errorf("Load() = %v, want ErrNotConfigured", err)
			}
			if verb == "list" && out.String() != "" {
				t.Errorf("list output = %q, want \"\"", out.String())
			}
		})
	}
}

// A token is made with the scopes asked for, in either order of flag and
// name, and none when none are asked for (#178). The list says which.
func TestRunToken_Scopes(t *testing.T) {
	dir := t.TempDir()
	old := state.Token{Name: "old", Hash: auth.HashToken("old-secret"), CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := state.NewStore(dir).Save(state.State{APITokens: []state.Token{old}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "otter", "--scope", "console"},
		{"add", "--scope=console", "otter2"},
		{"add", "ci"},
	} {
		if err := runToken(args, io.Discard, io.Discard, dir); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	st, err := state.NewStore(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, token := range st.APITokens {
		got[token.Name] = token.Scopes
	}
	want := map[string][]string{"old": nil, "otter": {"console"}, "otter2": {"console"}, "ci": {}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scopes = %#v, want %#v", got, want)
	}

	var out bytes.Buffer
	if err := runToken([]string{"list"}, &out, io.Discard, dir); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"old", "all (made before scopes)", "otter", "console", "ci"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("list = %q, missing %q", out.String(), line)
		}
	}
	if !regexp.MustCompile(`(?m)^ci\s+\S+\s+-\s*$`).MatchString(out.String()) {
		t.Errorf("list = %q, want ci's scopes as -", out.String())
	}
}
