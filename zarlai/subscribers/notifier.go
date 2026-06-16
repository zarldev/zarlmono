package subscribers

import (
	"context"
	"fmt"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/events"
)

// Notifier pushes frontend notifications for self-improvement events.
type Notifier struct {
	store *znotify.NotificationStore
}

func NewNotifier(store *znotify.NotificationStore) *Notifier {
	return &Notifier{store: store}
}

func (n *Notifier) Handle(_ context.Context, e events.Event) error {
	switch p := e.Payload.(type) {
	case events.ToolProposedPayload:
		n.store.Push(znotify.Notification{
			ToolName: "self_improvement",
			Content:  fmt.Sprintf("New tool proposed: %s — %s", p.ToolName, p.Rationale),
		})
	default:
		return fmt.Errorf("notifier: unexpected payload type %T", e.Payload)
	}
	return nil
}
