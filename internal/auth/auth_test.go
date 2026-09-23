package auth

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashAndVerify_RoundTrip(t *testing.T) {
	password := "the one ring"
	h, err := Hash(password)
	if err != nil {
		t.Fatalf("test hashing password: %v", err)
	}
	ok, err := Verify(password, h)
	if err != nil {
		t.Fatalf("test verifying password: %v", err)
	}
	if !ok {
		t.Error("the correct password should be accepted")
	}
	ok, err = Verify("wrong password", h)
	if err != nil {
		t.Fatalf("test verifying password: %v", err)
	}
	if ok {
		t.Error("the wrong password should be rejected")
	}
}

func TestVerify_StoredParameters(t *testing.T) {
	password := "the one ring"
	salt := []byte("test salt")
	hash := argon2.IDKey([]byte(password), salt, 1, 8192, 1, 24)
	encoded := "$argon2id$v=19$m=8192,t=1,p=1$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(hash)

	ok, err := Verify(password, encoded)
	if err != nil {
		t.Fatalf("verify with stored parameters: %v", err)
	}
	if !ok {
		t.Fatal("the correct password should be accepted with non-default stored parameters")
	}
}

func TestHash_DefaultParametersAndRandomSalt(t *testing.T) {
	first, err := Hash("the one ring")
	if err != nil {
		t.Fatalf("hash first password: %v", err)
	}
	second, err := Hash("the one ring")
	if err != nil {
		t.Fatalf("hash second password: %v", err)
	}
	const prefix = "$argon2id$v=19$m=65536,t=3,p=4$"
	for _, encoded := range []string{first, second} {
		if !strings.HasPrefix(encoded, prefix) {
			t.Errorf("expected default parameter prefix %q, got %q", prefix, encoded)
		}
	}
	if first == second {
		t.Error("hashing the same password twice should produce different encoded hashes")
	}
	if strings.Split(first, "$")[4] == strings.Split(second, "$")[4] {
		t.Error("hashing the same password twice should produce different salts")
	}
}

func TestParseEncodedString_Success(t *testing.T) {
	const encoded = "$argon2id$v=19$m=8192,t=1,p=2$c2FsdDEyMzQ$aGFzaDEyMzQ"
	want := hashParams{
		version:    19,
		mem:        8192,
		iterations: 1,
		threads:    2,
		salt:       []byte("salt1234"),
		hash:       []byte("hash1234"),
	}
	got, err := parseEncodedString(encoded)
	if err != nil {
		t.Fatalf("parse valid encoded string: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}

func TestParseEncodedString(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
		wantErr error
	}{
		{
			name:    "wrong number of parts",
			encoded: "$argon2id$v=1$m=2,t=3,p=4$salt",
			wantErr: ErrMalformed,
		},
		{
			name:    "unsupported hashing algorithm",
			encoded: "$argon3id$v=1$m=2,t=3,p=4$salt$pass",
			wantErr: ErrUnsupportedAlgorithm,
		},
		{
			name:    "invalid Argon2 version",
			encoded: "$argon2id$v1$m=2,t=3,p=4$salt$pass",
		},
		{
			name:    "unsupported Argon2 version",
			encoded: "$argon2id$v=1$m=2,t=3,p=4$salt$pass",
			wantErr: ErrUnsupportedVersion,
		},
		{
			name:    "invalid Argon2 parameters",
			encoded: "$argon2id$v=19$m=2,t=3$salt$pass",
			wantErr: ErrMalformed,
		},
		{
			name:    "unsupported Argon2 memory parameter -- lower bound",
			encoded: "$argon2id$v=19$m=8191,t=3,p=4$c2FsdDEyMzQ$aGFzaDEyMzQ",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported Argon2 memory parameter -- higher bound",
			encoded: "$argon2id$v=19$m=2097153,t=3,p=4$c2FsdDEyMzQ$aGFzaDEyMzQ",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported Argon2 iteration parameter -- lower bound",
			encoded: "$argon2id$v=19$m=8192,t=0,p=4$c2FsdDEyMzQ$aGFzaDEyMzQ",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported Argon2 iteration parameter -- higher bound",
			encoded: "$argon2id$v=19$m=8192,t=11,p=4$c2FsdDEyMzQ$aGFzaDEyMzQ",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported Argon2 parallelism parameter -- lower bound",
			encoded: "$argon2id$v=19$m=8192,t=1,p=0$c2FsdDEyMzQ$aGFzaDEyMzQ",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported Argon2 parallelism parameter -- higher bound",
			encoded: "$argon2id$v=19$m=8192,t=1,p=17$c2FsdDEyMzQ$aGFzaDEyMzQ",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "invalid salt encoding",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$!$AAAAAAAAAAA",
			wantErr: base64.CorruptInputError(0),
		},
		{
			name:    "unsupported salt length -- lower bound",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$" + base64.RawStdEncoding.EncodeToString(make([]byte, 7)) + "$AAAAAAAAAAA",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported salt length -- higher bound",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$" + base64.RawStdEncoding.EncodeToString(make([]byte, 65)) + "$AAAAAAAAAAA",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "empty salt",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$$AAAAAAAAAAA",
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "invalid hash encoding",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAA$!",
			wantErr: base64.CorruptInputError(0),
		},
		{
			name:    "unsupported hash length -- lower bound",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAA$" + base64.RawStdEncoding.EncodeToString(make([]byte, 7)),
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "unsupported hash length -- higher bound",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAA$" + base64.RawStdEncoding.EncodeToString(make([]byte, 65)),
			wantErr: ErrUnsupportedParams,
		},
		{
			name:    "empty hash",
			encoded: "$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAA$",
			wantErr: ErrUnsupportedParams,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseEncodedString(tt.encoded)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}
