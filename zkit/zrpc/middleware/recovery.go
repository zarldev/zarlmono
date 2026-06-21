package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"connectrpc.com/connect"
)

// Recovery returns a [connect.Interceptor] that catches panics from
// downstream handlers and turns them into connect.CodeInternal
// errors. Covers BOTH unary and streaming RPCs — unary panic
// recovery used to be the only path, leaving server-streaming and
// bidi-streaming methods exposed to whatever the runtime did on a
// raw panic (typically: torn-down stream, possibly leaked goroutines
// in the bidi case).
//
// The full stack trace is logged at error level (with the procedure
// name + panic value); the wire response gets a generic "internal
// server error" message so clients don't see implementation details.
//
// Pass nil for the default slog logger.
func Recovery(logger *slog.Logger) connect.Interceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return &recoveryInterceptor{logger: logger}
}

// recoveryInterceptor is the [connect.Interceptor] implementation.
// Each Wrap* method installs a deferred recover that turns a panic
// into a uniform "internal server error" — the panic detail goes
// to the log, never the wire.
type recoveryInterceptor struct {
	logger *slog.Logger
}

// WrapUnary covers ordinary request/response RPCs.
func (i *recoveryInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = i.handlePanic(ctx, req.Spec().Procedure, r)
				resp = nil
			}
		}()
		resp, err = next(ctx, req)
		return resp, err
	}
}

// WrapStreamingHandler covers server-streaming, client-streaming,
// and bidi-streaming server-side panics. The interceptor lives on
// the SERVER side; client-side stream panic recovery is the client
// caller's responsibility (a panic in a client goroutine is the
// caller's bug to fix, not transport machinery).
func (i *recoveryInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = i.handlePanic(ctx, conn.Spec().Procedure, r)
			}
		}()
		err = next(ctx, conn)
		return err
	}
}

// WrapStreamingClient is a passthrough — see WrapStreamingHandler's
// note. Client-side panics in user goroutines aren't this
// middleware's job to catch.
func (i *recoveryInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// handlePanic logs the panic + stack and returns a uniform
// connect.CodeInternal error. Shared by WrapUnary and
// WrapStreamingHandler so the wire shape is identical regardless of
// RPC kind.
func (i *recoveryInterceptor) handlePanic(ctx context.Context, procedure string, r any) error {
	i.logger.ErrorContext(ctx, "rpc panic recovered",
		"procedure", procedure,
		"panic", r,
		"stack", string(debug.Stack()))

	// panicErr is captured for the log line but never returned to
	// the client — the wire response is the generic message below.
	var panicErr error
	switch v := r.(type) {
	case error:
		panicErr = v
	default:
		panicErr = fmt.Errorf("%v", v)
	}
	_ = panicErr

	return connect.NewError(
		connect.CodeInternal,
		errors.New("internal server error"),
	)
}
