package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ottermq/jaildeck/internal/state"
)

var (
	ErrUnknownToken           = errors.New("unknown token")
	ErrTokenNameAlreadyExists = errors.New("token name already exists")
	ErrInvalidTokenName       = errors.New("token name invalid")
	ErrMissingTokenName       = errors.New("missing token name")
)

func GenerateToken() (string, error) {
	b := make([]byte, 32)

	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "jd_" + base64.RawURLEncoding.EncodeToString(b), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func MatchToken(presented string, tokens []state.Token) (state.Token, bool) {
	digest := HashToken(presented)
	for _, token := range tokens {
		if subtle.ConstantTimeCompare([]byte(digest), []byte(token.Hash)) == 1 {
			return token, true
		}
	}
	return state.Token{}, false
}

// AddToken makes a token with exactly the scopes given, none if none are: a
// token made since scopes exist has a list, so that it is never mistaken for
// one made before them, which keeps every scope (#178).
func AddToken(tokens []state.Token, name string, scopes []string, now time.Time) ([]state.Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return tokens, "", ErrMissingTokenName
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return tokens, "", fmt.Errorf("%w: name cannot contain control characters", ErrInvalidTokenName)
	}

	if !isTokenNameUnique(tokens, name) {
		return tokens, "", ErrTokenNameAlreadyExists
	}

	plainToken, err := GenerateToken()
	if err != nil {
		return tokens, "", err
	}
	hash := HashToken(plainToken)
	if scopes == nil {
		scopes = []string{}
	}
	tokens = append(tokens, state.Token{Name: name, Hash: hash, CreatedAt: now, Scopes: scopes})
	return tokens, plainToken, nil
}

func RevokeToken(tokens []state.Token, name string) ([]state.Token, error) {
	name = strings.TrimSpace(name)
	for i, token := range tokens {
		if strings.EqualFold(token.Name, name) {
			out := make([]state.Token, 0, len(tokens)-1)
			out = append(out, tokens[:i]...)
			return append(out, tokens[i+1:]...), nil
		}
	}
	return tokens, ErrUnknownToken // we return error here. Idempotency could be a choice on higher layers.
}

func isTokenNameUnique(tokens []state.Token, name string) bool {
	for _, token := range tokens {
		// Case-insensitive uniqueness -- deliberate decision since "backup"
		// and "Backup" would represent an accidental duplicate rather than
		// meaningfully different tokens.
		if strings.EqualFold(token.Name, name) {
			return false
		}
	}
	return true
}
