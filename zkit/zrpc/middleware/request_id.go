package middleware

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

// requestIDKey is the context key Logging and downstream handlers use
// to retrieve the per-request ID. Unexported so external code goes
// through RequestIDFromContext.
type requestIDKey struct{}

// RequestIDHeader is the HTTP header name the RequestID interceptor
// reads from (incoming) and writes to (outgoing). Conventionally
// "X-Request-ID"; override via WithRequestIDHeader if your gateway
// uses a different name.
const RequestIDHeader = "X-Request-ID"

// RequestID returns a unary interceptor that ensures every request
// carries an ID:
//
//   - If the incoming request has the X-Request-ID header set,
//     that value is used (lets a gateway / mesh propagate IDs).
//   - Otherwise a fresh UUIDv4 is generated.
//
// The ID is attached to the context (retrievable via
// RequestIDFromContext) and echoed in the response trailers so clients
// can correlate logs against their own request log.
func RequestID() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			id := req.Header().Get(RequestIDHeader)
			if id == "" {
				id = uuid.NewString()
			}
			ctx = context.WithValue(ctx, requestIDKey{}, id)

			resp, err := next(ctx, req)
			if resp != nil {
				resp.Header().Set(RequestIDHeader, id)
			}
			return resp, err
		}
	}
}

// RequestIDFromContext returns the request ID set by the RequestID
// interceptor, or "" if no ID is in the context.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
