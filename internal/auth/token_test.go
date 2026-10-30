package auth

import (
	"encoding/base64"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/state"
)

func TestGenerateToken(t *testing.T) {
	seen := make(map[string]bool)
	for range 10 {
		token, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		encoded, ok := strings.CutPrefix(token, "jd_")
		if !ok {
			t.Fatalf("token %q is missing jd_ prefix", token)
		}
		if len(encoded) != 43 {
			t.Errorf("encoded token length = %d, want 43", len(encoded))
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode token as unpadded URL-safe base64: %v", err)
		}
		if len(decoded) != 32 {
			t.Errorf("decoded token length = %d, want 32", len(decoded))
		}
		if seen[token] {
			t.Fatal("GenerateToken returned a duplicate token")
		}
		seen[token] = true
	}
}

func TestHashToken(t *testing.T) {
	for _, tt := range []struct{ name, token, want string }{
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"nonempty", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for range 2 {
				if got := HashToken(tt.token); got != tt.want {
					t.Errorf("HashToken(%q) = %q, want %q", tt.token, got, tt.want)
				}
			}
		})
	}
}

func TestMatchToken(t *testing.T) {
	first := state.Token{Name: "first", Hash: HashToken("jd_first"), CreatedAt: time.Unix(100, 0)}
	second := state.Token{Name: "second", Hash: HashToken("jd_second"), CreatedAt: time.Unix(200, 0)}
	duplicate := state.Token{Name: "duplicate", Hash: first.Hash}
	for _, tt := range []struct {
		name      string
		presented string
		tokens    []state.Token
		want      state.Token
		wantOK    bool
	}{
		{"nil tokens", "jd_first", nil, state.Token{}, false},
		{"empty tokens", "jd_first", []state.Token{}, state.Token{}, false},
		{"first match", "jd_first", []state.Token{first, second}, first, true},
		{"later match", "jd_second", []state.Token{first, second}, second, true},
		{"no match", "jd_unknown", []state.Token{first, second}, state.Token{}, false},
		{"empty presented", "", []state.Token{first}, state.Token{}, false},
		{"case sensitive", "JD_FIRST", []state.Token{first}, state.Token{}, false},
		{"hash is not a token", first.Hash, []state.Token{first}, state.Token{}, false},
		{"malformed hashes", "jd_first", []state.Token{{Hash: ""}, {Hash: "invalid"}}, state.Token{}, false},
		{"skip malformed hash", "jd_first", []state.Token{{Hash: "invalid"}, first}, first, true},
		{"first duplicate wins", "jd_first", []state.Token{first, duplicate}, first, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MatchToken(tt.presented, tt.tokens)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MatchToken = (%+v, %t), want (%+v, %t)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestAddToken(t *testing.T) {
	now := time.Unix(200, 0)
	original := []state.Token{{Name: "backup", Hash: HashToken("jd_existing"), CreatedAt: time.Unix(100, 0)}}
	for _, tt := range []struct {
		name, input string
		wantErr     error
	}{
		{"empty", "", ErrMissingTokenName},
		{"whitespace", " \t\n", ErrMissingTokenName},
		{"control character", "new\x00token", ErrInvalidTokenName},
		{"embedded newline", "new\ntoken", ErrInvalidTokenName},
		{"duplicate", "backup", ErrTokenNameAlreadyExists},
		{"duplicate case", "BACKUP", ErrTokenNameAlreadyExists},
		{"duplicate trimmed", " backup ", ErrTokenNameAlreadyExists},
		{"success", " \tnew token\n", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tokens := slices.Clone(original)
			got, plaintext, err := AddToken(tokens, tt.input, nil, now)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddToken error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if !reflect.DeepEqual(got, original) {
					t.Errorf("tokens on error = %+v, want %+v", got, original)
				}
				if plaintext != "" {
					t.Errorf("plaintext on error = %q, want empty", plaintext)
				}
				return
			}
			if len(got) != len(original)+1 {
				t.Fatalf("token count = %d, want %d", len(got), len(original)+1)
			}
			if !reflect.DeepEqual(got[:len(original)], original) {
				t.Errorf("existing tokens changed: %+v", got)
			}
			if plaintext == "" {
				t.Fatal("AddToken returned empty plaintext")
			}
			want := state.Token{Name: "new token", Hash: HashToken(plaintext), CreatedAt: now, Scopes: []string{}}
			if added := got[len(original)]; !reflect.DeepEqual(added, want) {
				t.Errorf("added token = %+v, want %+v", added, want)
			}
			for _, token := range got {
				for _, field := range []string{token.Name, token.Hash, token.CreatedAt.String()} {
					if strings.Contains(field, plaintext) {
						t.Errorf("stored field %q contains plaintext token", field)
					}
				}
			}
		})
	}
}

func TestRevokeToken(t *testing.T) {
	original := []state.Token{
		{Name: "first", Hash: HashToken("jd_first"), CreatedAt: time.Unix(100, 0)},
		{Name: "second", Hash: HashToken("jd_second"), CreatedAt: time.Unix(200, 0)},
		{Name: "third", Hash: HashToken("jd_third"), CreatedAt: time.Unix(300, 0)},
	}
	for _, tt := range []struct {
		name, input string
		want        []state.Token
		wantErr     error
	}{
		{"first", "first", original[1:], nil},
		{"middle", "second", []state.Token{original[0], original[2]}, nil},
		{"last", "third", original[:2], nil},
		{"case insensitive", "SECOND", []state.Token{original[0], original[2]}, nil},
		{"trimmed", " \tsecond\n", []state.Token{original[0], original[2]}, nil},
		{"unknown", "missing", original, ErrUnknownToken},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tokens := slices.Clone(original)
			got, err := RevokeToken(tokens, tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RevokeToken error = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RevokeToken = %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(tokens, original) {
				t.Errorf("caller's tokens changed: %+v, want %+v", tokens, original)
			}
			if err == nil && len(got) > 0 {
				got[0].Name = "changed"
				if !reflect.DeepEqual(tokens, original) {
					t.Errorf("result aliases caller's tokens: %+v", tokens)
				}
			}
		})
	}
}
