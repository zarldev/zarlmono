package service

import "context"

// ContextKey is the type for zarlai's request-scoped context values.
type ContextKey string

const (
	CtxPersonName ContextKey = "person_name"
	CtxSessionID  ContextKey = "session_id"
)

// PersonNameFromCtx returns the person name stored on ctx, or "" if unset.
func PersonNameFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(CtxPersonName).(string)
	return v
}

// SessionIDFromCtx returns the session id stored on ctx, or "" if unset.
func SessionIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(CtxSessionID).(string)
	return v
}
