package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/zarldev/zarlmono/zkit/zrpc/middleware"
)

func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	t.Parallel()

	called := false
	var seenID string

	interceptor := middleware.RequestID()
	handler := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		seenID = middleware.RequestIDFromContext(ctx)
		return connect.NewResponse[any](nil), nil
	})

	req := connect.NewRequest[any](nil)
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called {
		t.Fatal("downstream handler not called")
	}
	if seenID == "" {
		t.Error("RequestIDFromContext returned empty ID; should have been generated")
	}
	if got := resp.Header().Get(middleware.RequestIDHeader); got != seenID {
		t.Errorf("response header %s = %q, want %q", middleware.RequestIDHeader, got, seenID)
	}
}

func TestRequestID_PropagatesIncoming(t *testing.T) {
	t.Parallel()

	const incoming = "test-trace-id-12345"
	var seenID string

	interceptor := middleware.RequestID()
	handler := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		seenID = middleware.RequestIDFromContext(ctx)
		return connect.NewResponse[any](nil), nil
	})

	req := connect.NewRequest[any](nil)
	req.Header().Set(middleware.RequestIDHeader, incoming)

	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if seenID != incoming {
		t.Errorf("seen ID = %q, want %q", seenID, incoming)
	}
	if got := resp.Header().Get(middleware.RequestIDHeader); got != incoming {
		t.Errorf("response header = %q, want %q", got, incoming)
	}
}

func TestRecovery_CatchesPanic(t *testing.T) {
	t.Parallel()

	interceptor := middleware.Recovery(nil)
	handler := interceptor.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		panic("explosion in business logic")
	})

	req := connect.NewRequest[any](nil)
	_, err := handler(context.Background(), req)
	if err == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Errorf("code = %v, want CodeInternal", connectErr.Code())
	}
	if !strings.Contains(connectErr.Error(), "internal server error") {
		t.Errorf("error message = %q, want it to mention 'internal server error'", connectErr.Error())
	}
}

func TestRecovery_PassesNonPanicErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("ordinary failure")
	interceptor := middleware.Recovery(nil)
	handler := interceptor.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, want
	})

	req := connect.NewRequest[any](nil)
	_, err := handler(context.Background(), req)
	if !errors.Is(err, want) {
		t.Errorf("got err = %v, want %v (verbatim)", err, want)
	}
}

// TestRecovery_StreamingHandlerCatchesPanic guards the streaming
// half of the recovery interceptor. Earlier it was unary-only and a
// panic in a streaming handler tore the stream down with whatever
// the runtime chose (typically a stack trace + abrupt close). Now
// streaming panics surface as the same "internal server error" the
// unary path produces.
func TestRecovery_StreamingHandlerCatchesPanic(t *testing.T) {
	t.Parallel()

	interceptor := middleware.Recovery(nil)
	handler := interceptor.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		panic("explosion in streaming handler")
	})

	err := handler(context.Background(), fakeStreamingConn{})
	if err == nil {
		t.Fatal("expected error from recovered streaming panic, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Errorf("code = %v, want CodeInternal", connectErr.Code())
	}
	if !strings.Contains(connectErr.Error(), "internal server error") {
		t.Errorf("error message = %q, want it to mention 'internal server error'", connectErr.Error())
	}
}

// fakeStreamingConn is a minimal connect.StreamingHandlerConn stand-
// in for the panic test. Only Spec() is reached on the recovery
// path; the rest panic if called (which is fine — they shouldn't be
// for the under-test panic flow).
type fakeStreamingConn struct{}

func (fakeStreamingConn) Spec() connect.Spec {
	return connect.Spec{Procedure: "/test.Service/Stream"}
}
func (fakeStreamingConn) Peer() connect.Peer           { return connect.Peer{} }
func (fakeStreamingConn) Receive(any) error            { panic("unused") }
func (fakeStreamingConn) RequestHeader() http.Header   { return http.Header{} }
func (fakeStreamingConn) Send(any) error               { panic("unused") }
func (fakeStreamingConn) ResponseHeader() http.Header  { return http.Header{} }
func (fakeStreamingConn) ResponseTrailer() http.Header { return http.Header{} }
func (fakeStreamingConn) Conn() (any, error)           { return struct{}{}, nil }

func TestLogging_DoesNotAlterResult(t *testing.T) {
	t.Parallel()

	want := errors.New("downstream failure")
	interceptor := middleware.Logging(nil)
	handler := interceptor(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, want
	})

	req := connect.NewRequest[any](nil)
	_, err := handler(context.Background(), req)
	if !errors.Is(err, want) {
		t.Errorf("got err = %v, want %v", err, want)
	}
}
