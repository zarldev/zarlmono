package service_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
)

func TestContextKeys(t *testing.T) {
	ctx := t.Context()

	// Test empty context
	if name := service.PersonNameFromCtx(ctx); name != "" {
		t.Errorf("PersonNameFromCtx with empty context = %q, want empty string", name)
	}
	if sid := service.SessionIDFromCtx(ctx); sid != "" {
		t.Errorf("SessionIDFromCtx with empty context = %q, want empty string", sid)
	}

	// Test with values set
	ctx = context.WithValue(ctx, service.CtxPersonName, "Alice")
	ctx = context.WithValue(ctx, service.CtxSessionID, "session-123")

	if name := service.PersonNameFromCtx(ctx); name != "Alice" {
		t.Errorf("PersonNameFromCtx = %q, want Alice", name)
	}
	if sid := service.SessionIDFromCtx(ctx); sid != "session-123" {
		t.Errorf("SessionIDFromCtx = %q, want session-123", sid)
	}
}
