package znotify_test

import (
	"context"
	"fmt"

	notify "github.com/zarldev/zarlmono/zkit/znotify"
)

// A notification pushed before a subscriber attaches is queued.
// Subscribe and Drain to collect the backlog when the consumer
// reconnects.
func ExampleNotificationStore() {
	store := notify.NewNotificationStore()

	// Producer pushes while no subscriber is live.
	store.Push(notify.Notification{SessionID: "alice", Content: "task done"})

	// Consumer attaches later. Live subscribers receive Push immediately;
	// pre-existing items wait in Drain.
	store.Subscribe(context.Background(), "alice")
	pending := store.Drain("alice")
	fmt.Println(len(pending), pending[0].Content)
	// Output: 1 task done
}

// Subscribe tied to a context: when the context cancels, the
// subscription is removed and the channel closes.
func ExampleNotificationStore_subscribeCtx() {
	store := notify.NewNotificationStore()
	ctx, cancel := context.WithCancel(context.Background())

	ch := store.Subscribe(ctx, "alice")

	store.Push(notify.Notification{SessionID: "alice", Content: "live!"})
	n := <-ch
	fmt.Println(n.Content)

	cancel() // auto-unsubscribes; ch will close
	// Output: live!
}
