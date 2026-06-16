// Package middleware provides ConnectRPC interceptors that any service
// will want: request-scoped logging, panic recovery, request ID
// injection.
//
// Each interceptor is independently useful — apply them in whatever
// order you want, but the conventional stack is RequestID → Logging →
// Recovery (outermost first), so log lines carry the request ID and a
// panic in business logic still produces a structured error response.
package middleware

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
)

// Logging returns a unary interceptor that emits one log record per
// RPC at start (info) and one at end (info on success, error on
// failure). The procedure name and elapsed duration are always
// included; on error the underlying error message is too.
//
// Pass nil for the default slog logger.
func Logging(logger *slog.Logger) connect.UnaryInterceptorFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			procedure := req.Spec().Procedure
			logger.InfoContext(ctx, "rpc start", "procedure", procedure)

			resp, err := next(ctx, req)
			elapsed := time.Since(start)

			if err != nil {
				logger.ErrorContext(ctx, "rpc error",
					"procedure", procedure,
					"duration", elapsed,
					"error", err)
				return resp, err
			}
			logger.InfoContext(ctx, "rpc done",
				"procedure", procedure,
				"duration", elapsed)
			return resp, nil
		}
	}
}
