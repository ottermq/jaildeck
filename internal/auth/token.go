package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"

	"codeberg.org/Otter-Labs/jaildeck/internal/state"
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
