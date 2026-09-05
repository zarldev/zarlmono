package messagebus_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/messagebus"
)

func TestMemoryQueueGroups(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"sync", "async"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				bus := messagebus.NewMemoryBus[int]()
				if mode == "sync" {
					bus = messagebus.NewMemoryBus[int](messagebus.WithSynchronous[int]())
				}
				t.Cleanup(func() { _ = bus.Close() })
				var mu sync.Mutex
				counts := make(map[string]map[int]int)
				handler := func(label string) messagebus.Handler[int] {
					return func(_ context.Context, msg messagebus.Message[int]) error {
						mu.Lock()
						defer mu.Unlock()
						if counts[label] == nil {
							counts[label] = make(map[int]int)
						}
						counts[label][msg.Data]++
						return nil
					}
				}
				for _, tc := range []struct{ subject, queue, label string }{
					{"jobs.*", "workers", "workers"},
					{"jobs.created", "workers", "workers"},
					{"jobs.>", "audit", "audit"},
					{"jobs.created", "audit", "audit"},
					{"other.*", "workers", "unmatched"},
					{"jobs.*", "", "broadcast1"},
					{"jobs.created", "", "broadcast2"},
				} {
					if _, err := bus.QueueSubscribe(t.Context(), tc.subject, tc.queue, handler(tc.label)); err != nil {
						t.Fatal(err)
					}
				}
				var publishers sync.WaitGroup
				for id := range 30 {
					publishers.Go(func() {
						if err := bus.Publish(t.Context(), "jobs.created", id); err != nil {
							t.Error(err)
						}
					})
				}
				publishers.Wait()
				synctest.Wait()
				mu.Lock()
				defer mu.Unlock()
				for _, label := range []string{"workers", "audit", "broadcast1", "broadcast2"} {
					for id := range 30 {
						if got := counts[label][id]; got != 1 {
							t.Errorf("%s message %d: delivered %d times, want 1", label, id, got)
						}
					}
				}
				if len(counts["unmatched"]) != 0 {
					t.Error("unmatched queue member received messages")
				}
			})
		})
	}
}

func TestMemoryQueueMembershipRemoval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		bus := messagebus.NewMemoryBus[int](messagebus.WithSynchronous[int]())
		t.Cleanup(func() { _ = bus.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		removed := func(context.Context, messagebus.Message[int]) error {
			t.Error("removed queue member received a message")
			return nil
		}
		sub, err := bus.QueueSubscribe(t.Context(), "jobs.*", "workers", removed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bus.QueueSubscribe(ctx, "jobs.>", "workers", removed); err != nil {
			t.Fatal(err)
		}
		if err := sub.Unsubscribe(); err != nil {
			t.Fatal(err)
		}
		cancel()
		synctest.Wait()
		count := 0
		if _, err := bus.QueueSubscribe(t.Context(), "jobs.created", "workers", func(context.Context, messagebus.Message[int]) error {
			count++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for id := range 10 {
			if err := bus.Publish(t.Context(), "jobs.created", id); err != nil {
				t.Fatal(err)
			}
		}
		if count != 10 {
			t.Fatalf("remaining member received %d messages, want 10", count)
		}
	})
}
