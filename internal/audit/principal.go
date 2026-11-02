package audit

import "context"

type principalKey struct{}

// ContextWithPrincipal carries who a request authenticated as ("session",
// or "token:<name>") down to whatever logs an audit entry, as the request id
// is carried: so that every entry the request writes names it, without each
// call site threading it through.
func ContextWithPrincipal(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFromContext returns the principal, or "" for work no request
// started, such as what the observer sees change on the host.
func PrincipalFromContext(ctx context.Context) string {
	principal, _ := ctx.Value(principalKey{}).(string)
	return principal
}
