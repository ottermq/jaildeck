package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	memory      uint32 = 65536 // 64MiB
	iterations  uint32 = 3
	parallelism uint8  = 4
	saltLen            = 16
	keyLen      uint32 = 32
)

var (
	ErrMalformed            = errors.New("invalid encoded hash format")
	ErrUnsupportedAlgorithm = errors.New("unsupported hashing algorithm")
	ErrUnsupportedVersion   = errors.New("unsupported Argon2 version")
	// Sanity bounds, not a policy control
	ErrUnsupportedParams = errors.New("unsupported Argon2 parameters")
)

type hashParams struct {
	version    int
	mem        uint32
	iterations uint32
	threads    uint8
	salt       []byte
	hash       []byte
}

func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)

	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		iterations,
		memory,
		parallelism,
		keyLen,
	)
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedHash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory,
		iterations,
		parallelism,
		encodedSalt,
		encodedHash,
	)
	return encoded, nil
}

func Verify(password, stored string) (bool, error) {
	params, err := parseEncodedString(stored)
	if err != nil {
		return false, fmt.Errorf("parse stored hash: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		params.salt,
		params.iterations,
		params.mem,
		params.threads,
		uint32(len(params.hash)),
	)

	return subtle.ConstantTimeCompare(hash, params.hash) == 1, nil
}

func parseEncodedString(encoded string) (hashParams, error) {
	parts := strings.Split(encoded, "$")

	if len(parts) != 6 {
		return hashParams{}, ErrMalformed
	}
	if parts[1] != "argon2id" {
		return hashParams{}, ErrUnsupportedAlgorithm
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return hashParams{}, fmt.Errorf("parse Argon2 version: %w", err)
	}
	if version != argon2.Version {
		return hashParams{}, ErrUnsupportedVersion
	}

	var parsedMem, parsedIterations uint32
	var parsedThreads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parsedMem, &parsedIterations, &parsedThreads); err != nil {
		return hashParams{}, ErrMalformed
	}
	if parsedMem < 8*1024 || parsedMem > 2*1024*1024 {
		return hashParams{}, ErrUnsupportedParams
	}
	if parsedIterations < 1 || parsedIterations > 10 {
		return hashParams{}, ErrUnsupportedParams
	}
	if parsedThreads < 1 || parsedThreads > 16 {
		return hashParams{}, ErrUnsupportedParams
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return hashParams{}, fmt.Errorf("decode salt: %w", err)
	}
	if len(salt) < 8 || len(salt) > 64 {
		return hashParams{}, ErrUnsupportedParams
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return hashParams{}, fmt.Errorf("decode hash: %w", err)
	}
	if len(hash) < 8 || len(hash) > 64 {
		return hashParams{}, ErrUnsupportedParams
	}

	params := hashParams{
		version:    version,
		mem:        parsedMem,
		iterations: parsedIterations,
		threads:    parsedThreads,
		salt:       salt,
		hash:       hash,
	}
	return params, nil
}
