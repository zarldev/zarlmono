package timer_test

import (
	"context"
	"testing"
	"time"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/timer"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func sessionCtx(t *testing.T, id string) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), service.CtxSessionID, id)
}

func TestTimerToolSetAndFire(t *testing.T) {
	ns := znotify.NewNotificationStore()
	tool := timer.NewTimerTool(ns)

	ctx := sessionCtx(t, "sess-1")
	result, err := tool.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{
		"duration": "100ms",
		"label":    "tea",
	}})

	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if content == "" {
		t.Fatal("expected non-empty content")
	}

	for _, want := range []string{"Timer set", "tea"} {
		if !contains(content, want) {
			t.Errorf("result content = %q, want substring %q", content, want)
		}
	}

	time.Sleep(200 * time.Millisecond)

	notifications := ns.Drain("sess-1")
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}

	n := notifications[0]
	for _, want := range []string{"Timer complete", "tea"} {
		if !contains(n.Content, want) {
			t.Errorf("notification.Content = %q, want substring %q", n.Content, want)
		}
	}
}

// TestTimerToolFiresOnDifferentSubscriber covers the failure mode the
// user reported: a timer set in session A fires after the user has
// reloaded the page (now subscribed under session B). With Broadcast,
// the live subscriber on B receives the notification — without it, the
// notification only landed in pending[A] and the user never heard it.
func TestTimerToolFiresOnDifferentSubscriber(t *testing.T) {
	ns := znotify.NewNotificationStore()
	tool := timer.NewTimerTool(ns)

	originalCtx := sessionCtx(t, "sess-original")
	r, err := tool.Execute(originalCtx, tools.ToolCall{Arguments: tools.ToolParameters{
		"duration": "50ms",
		"label":    "tea",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !r.Success {
		t.Fatalf("set timer: %s", r.Error)
	}

	// User reloaded the page — original session is no longer
	// subscribed; a new session subscribes instead.
	freshCh := ns.Subscribe(t.Context(), "sess-fresh")
	t.Cleanup(func() { ns.Unsubscribe("sess-fresh", freshCh) })

	select {
	case n := <-freshCh:
		if !contains(n.Content, "Timer complete") {
			t.Errorf("notification.Content = %q, want 'Timer complete'", n.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fresh subscriber did not receive timer notification within 500ms")
	}
}

func TestStatusToolShowsRemaining(t *testing.T) {
	ns := znotify.NewNotificationStore()
	tt := timer.NewTimerTool(ns)
	status := timer.NewStatusTool(tt)

	ctx := sessionCtx(t, "sess-1")

	// No timers
	result, err := status.Execute(ctx, tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	if !contains(service.ToolResultText(result), "No active timers") {
		t.Errorf("expected no timers message, got %q", service.ToolResultText(result))
	}

	// Set a timer
	if _, err := tt.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"duration": "10s", "label": "eggs"}}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	result, err = status.Execute(ctx, tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if !contains(content, "eggs") {
		t.Errorf("expected label in status, got %q", content)
	}
	if !contains(content, "remaining") {
		t.Errorf("expected 'remaining' in status, got %q", content)
	}
}

func TestTimerToolInvalidDuration(t *testing.T) {
	ns := znotify.NewNotificationStore()
	tool := timer.NewTimerTool(ns)

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"duration": "not-a-duration",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Success {
		t.Fatalf("expected failure, got %q", service.ToolResultText(result))
	}
}

func TestTimerToolNegativeDuration(t *testing.T) {
	ns := znotify.NewNotificationStore()
	tool := timer.NewTimerTool(ns)

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"duration": "-5s",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Success {
		t.Fatalf("expected failure, got %q", service.ToolResultText(result))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexString(s, sub) >= 0)
}

func indexString(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
