package messagebus_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/messagebus"
)

func noopHandler(context.Context, messagebus.Message[int]) error { return nil }

// TestMemoryBus_UnsubscribeAfterCloseNoPanic locks the double-close fix: Close
// closes every subscription channel; a later Unsubscribe (e.g. the deferred one
// in Request) must not close it again ("close of closed channel" panic).
func TestMemoryBus_UnsubscribeAfterCloseNoPanic(t *testing.T) {
	t.Parallel()
	bus := messagebus.NewMemoryBus[int]()
	sub, err := bus.Subscribe(t.Context(), "x", noopHandler)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil { // must not panic
		t.Fatalf("Unsubscribe after Close: %v", err)
	}
}

// TestMemoryBus_DoubleUnsubscribeNoPanic: Unsubscribe must be idempotent.
func TestMemoryBus_DoubleUnsubscribeNoPanic(t *testing.T) {
	t.Parallel()
	bus := messagebus.NewMemoryBus[int]()
	sub, err := bus.Subscribe(t.Context(), "x", noopHandler)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("first Unsubscribe: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil { // must not panic
		t.Fatalf("second Unsubscribe: %v", err)
	}
}

// TestMemoryBus_CtxCancelAutoUnsubscribes locks the goroutine-leak fix: a
// cancelled subscribe context auto-unsubscribes (via context.AfterFunc), which
// closes the channel and lets processMessages exit — proven here by the sub
// going invalid without any explicit Unsubscribe.
func TestMemoryBus_CtxCancelAutoUnsubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bus := messagebus.NewMemoryBus[int]()
		ctx, cancel := context.WithCancel(t.Context())
		sub, err := bus.Subscribe(ctx, "x", noopHandler)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if !sub.IsValid() {
			t.Fatal("sub should be valid before cancel")
		}
		cancel()
		synctest.Wait()
		if sub.IsValid() {
			t.Fatal("ctx cancel did not auto-unsubscribe — the processMessages goroutine leaks")
		}
	})
}

func TestMemoryBus_CloseCancelsAndWaitsForDelivery(t *testing.T) {
	bus := messagebus.NewMemoryBus[int]()
	started := make(chan struct{})
	exited := make(chan struct{})
	if _, err := bus.Subscribe(t.Context(), "x", func(ctx context.Context, _ messagebus.Message[int]) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := bus.Publish(t.Context(), "x", 1); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- bus.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for cancellation-aware delivery")
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned before the delivery goroutine exited")
	}
}
