// Binary notify_drain demonstrates live subscription and offline drain
// semantics without worker goroutines or external services.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/znotify"
)

func main() {
	store := znotify.NewNotificationStore()

	// No subscriber exists, so this notification enters the offline queue.
	store.Push(znotify.Notification{SessionID: "build-42", Content: "queued while offline"})

	// The caller owns the subscription lifetime and explicitly closes it.
	live := store.Subscribe(context.Background(), "build-42")
	store.Push(znotify.Notification{SessionID: "build-42", Content: "delivered live"})
	fmt.Fprintf(os.Stdout, "live=%q\n", (<-live).Content)
	store.Unsubscribe("build-42", live)
	_, open := <-live
	fmt.Fprintf(os.Stdout, "subscription_closed=%t\n", !open)

	pending := store.Drain("build-42")
	fmt.Fprintf(os.Stdout, "drained=%d content=%q\n", len(pending), pending[0].Content)
	fmt.Fprintf(os.Stdout, "drained_again=%d\n", len(store.Drain("build-42")))
}
