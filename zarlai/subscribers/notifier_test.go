package subscribers_test

import (
	"testing"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/subscribers"
)

func TestNotifier_sends_notification_on_tool_proposed(t *testing.T) {
	store := znotify.NewNotificationStore()
	h := subscribers.NewNotifier(store)

	err := h.Handle(t.Context(), events.Event{
		Type: events.ToolProposed,
		Payload: events.ToolProposedPayload{
			ToolName:  "weather",
			Rationale: "User asked about weather multiple times",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	notifications := store.Drain("")
	if len(notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifications))
	}
}
