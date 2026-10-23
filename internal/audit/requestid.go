package audit

import (
	"context"
	"fmt"
)

// RequestIDHeader is the header a client sets to correlate its own job with the
// audit entries one request produced (JD-016).
const RequestIDHeader = "X-Request-Id"

// MaxRequestIDLen bounds what goes into every audit line the request writes. 64
// is comfortably more than the forms a client actually sends — a hyphenated
// UUID is 36 characters and a ULID is 26 — and short enough that the header
// cannot be used to pad the log.
const MaxRequestIDLen = 64

type requestIDKey struct{}

// ContextWithRequestID carries a request id down to whatever logs an audit
// entry, so no call site has to thread it through its own signature.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the id, or "" when the request set none.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// ValidateRequestID accepts an id made of ASCII letters, digits and -._:, which
// covers a UUID, a ULID, and the "trace:span" shapes a tracing client sends. An
// empty header is not an error: it means the client set none.
//
// Anything else is refused rather than sanitised. The id is written verbatim to
// every audit line the request produces, and a line of JSON is a poor place to
// discover that a client sent a newline.
func ValidateRequestID(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > MaxRequestIDLen {
		return "", fmt.Errorf("%s is %d characters, over the %d maximum", RequestIDHeader, len(raw), MaxRequestIDLen)
	}
	for i := 0; i < len(raw); i++ {
		if !isRequestIDByte(raw[i]) {
			return "", fmt.Errorf("%s holds a character that is not a letter, digit, or one of -._: at offset %d", RequestIDHeader, i)
		}
	}
	return raw, nil
}

func isRequestIDByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-', c == '.', c == '_', c == ':':
		return true
	default:
		return false
	}
}
