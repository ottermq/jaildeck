package vms

import (
	"fmt"
	"net/http"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/ottermq/jaildeck/internal/common"
)

// A console password's bounds. bcrypt reads no more than 72 bytes, and
// x/crypto refuses a longer one rather than cut it short.
const (
	minConsolePassword = 8
	maxConsolePassword = 72
)

// checkConsolePassword refuses what a login prompt or the seed would get
// wrong: a control character can't be typed at a console, and a newline
// would end the password early. Nothing of it is in the answer.
func checkConsolePassword(password string) error {
	bad := func(format string, args ...any) error {
		return common.NewAppError(http.StatusBadRequest, fmt.Sprintf(format, args...))
	}
	if !utf8.ValidString(password) {
		return bad("console_password is not valid UTF-8")
	}
	if len(password) < minConsolePassword || len(password) > maxConsolePassword {
		return bad("console_password is %d bytes; give from %d to %d", len(password), minConsolePassword, maxConsolePassword)
	}
	for _, r := range password {
		if unicode.IsControl(r) {
			return bad("console_password has a control character, which a console can't type")
		}
	}
	return nil
}

// hashConsolePassword is the password as the guest's crypt(3) checks it.
// bcrypt, not SHA-512 crypt: x/crypto has it already, and Debian's libxcrypt
// takes its $2a$ form (measured on Debian 13, JD-038 amendment).
func hashConsolePassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", common.NewAppError(http.StatusInternalServerError, "could not hash the console password: "+err.Error())
	}
	return string(hash), nil
}

// comparePassword holds a PUT's console password against the hash in the
// guest's seed. The difference says only whether one is set: neither the
// password nor its hash is ever answered.
func comparePassword(want, hash string) (Difference, bool) {
	d := Difference{Field: "console_password"}
	switch {
	case want == "" && hash == "":
		return d, true
	case want == "":
		d.Want, d.Have = "none", "set"
	case hash == "":
		d.Want, d.Have = "set", "none"
	case bcrypt.CompareHashAndPassword([]byte(hash), []byte(want)) != nil:
		d.Want, d.Have = "set", "another"
	default:
		return d, true
	}
	return d, false
}
