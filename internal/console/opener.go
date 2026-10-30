package console

import (
	"fmt"
	"net/http"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/ottermq/jaildeck/internal/common"
)

// Opener is who opened a console and what they said about it, for the
// console-open audit entry (#155).
type Opener struct {
	// "session" or "token:<name>": who Jail Deck authenticated.
	Principal string
	// On whose behalf, as the client names them: a platform's user id or an
	// email. Never required, and never checked against anything.
	Actor string
	// Why, in the client's words.
	Reason string
}

// The bounds on what a client says, as for X-Request-Id: written verbatim
// into the audit log, so refused rather than sanitised when out of shape.
const (
	MaxActorLen  = 128
	MaxReasonLen = 256
)

// openerOf reads the optional actor and reason from the query. A WebSocket
// upgrade carries no body, and a browser cannot set a header on one.
func openerOf(r *http.Request, principal string) (Opener, error) {
	q := r.URL.Query()
	actor, reason := q.Get("actor"), q.Get("reason")
	if len(actor) > MaxActorLen {
		return Opener{}, badOpener("actor", fmt.Sprintf("is %d bytes, over the %d maximum", len(actor), MaxActorLen))
	}
	for i := 0; i < len(actor); i++ {
		if !isActorByte(actor[i]) {
			return Opener{}, badOpener("actor", fmt.Sprintf(
				"holds a character that is not a letter, digit, or one of -._:@/+ at offset %d", i))
		}
	}
	if len(reason) > MaxReasonLen {
		return Opener{}, badOpener("reason", fmt.Sprintf("is %d bytes, over the %d maximum", len(reason), MaxReasonLen))
	}
	if !utf8.ValidString(reason) {
		return Opener{}, badOpener("reason", "is not valid UTF-8")
	}
	for i, c := range reason {
		// Spaces and any script's letters, but no control characters: a line
		// of JSON is a poor place to find a newline.
		if !unicode.IsPrint(c) {
			return Opener{}, badOpener("reason", "holds a control character at offset "+strconv.Itoa(i))
		}
	}
	return Opener{Principal: principal, Actor: actor, Reason: reason}, nil
}

func badOpener(field, problem string) error {
	return common.NewAppError(http.StatusBadRequest, field+" "+problem)
}

func isActorByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '.', c == '_', c == ':', c == '@', c == '/', c == '+':
		return true
	default:
		return false
	}
}
