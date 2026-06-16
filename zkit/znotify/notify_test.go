package znotify_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	notify "github.com/zarldev/zarlmono/zkit/znotify"
)

func TestNotificationStore_LiveSubscriberReceivesPush(t *testing.T) {
	t.Parallel()

	store := notify.NewNotificationStore()
	ch := store.Subscribe(t.Context(), "session-A")
	t.Cleanup(func() { store.Unsubscribe("session-A", ch) })

	store.Push(notify.Notification{SessionID: "session-A", Content: "hello"})

	select {
	case n := <-ch:
		if n.Content != "hello" {
			t.Errorf("Content = %q", n.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive notification within 500ms")
	}
}

func TestNotificationStore_OfflineSessionQueuesPush(t *testing.T) {
	t.Parallel()

	store := notify.NewNotificationStore()
	store.Push(notify.Notification{SessionID: "offline", Content: "queued-1"})
	store.Push(notify.Notification{SessionID: "offline", Content: "queued-2"})

	got := store.Drain("offline")
	if len(got) != 2 {
		t.Fatalf("drain len = %d, want 2", len(got))
	}
	if got[0].Content != "queued-1" || got[1].Content != "queued-2" {
		t.Errorf("got = %+v", got)
	}

	// Drain clears.
	if again := store.Drain("offline"); len(again) != 0 {
		t.Errorf("second drain returned %d, want 0", len(again))
	}
}

func TestNotificationStore_BroadcastFansOutAndQueuesOriginating(t *testing.T) {
	t.Parallel()

	store := notify.NewNotificationStore()
	chA := store.Subscribe(t.Context(), "A")
	chB := store.Subscribe(t.Context(), "B")
	t.Cleanup(func() {
		store.Unsubscribe("A", chA)
		store.Unsubscribe("B", chB)
	})

	store.Push(notify.Notification{
		SessionID: "A",
		Content:   "global",
		Broadcast: true,
	})

	// Both subscribers see it.
	for _, want := range []struct {
		ch  <-chan notify.Notification
		sid string
	}{{chA, "A"}, {chB, "B"}} {
		select {
		case n := <-want.ch:
			if n.Content != "global" {
				t.Errorf("session %s Content = %q", want.sid, n.Content)
			}
			if n.SessionID != want.sid {
				t.Errorf("session %s SessionID = %q, want %q", want.sid, n.SessionID, want.sid)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("session %s did not receive broadcast", want.sid)
		}
	}

	// Originating session ALSO gets a queued copy (for context-reconstruction
	// paths that iterate Drain even when subscribers are live).
	if pending := store.Drain("A"); len(pending) != 1 || pending[0].Content != "global" {
		t.Errorf("originating session pending = %+v, want one queued copy", pending)
	}
}

func TestNotificationStore_BroadcastReachesAllKnownSessions(t *testing.T) {
	t.Parallel()

	store := notify.NewNotificationStore()
	// Set up: one live, one with stale pending, none subscribed.
	chLive := store.Subscribe(t.Context(), "live")
	t.Cleanup(func() { store.Unsubscribe("live", chLive) })
	store.Push(notify.Notification{SessionID: "stale", Content: "old"}) // queues on stale

	store.Broadcast(notify.Notification{Content: "tool installed"})

	// Live subscriber received it.
	select {
	case n := <-chLive:
		if n.Content != "tool installed" {
			t.Errorf("live got %q", n.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("live did not receive broadcast")
	}

	// Stale session's queue contains both old and broadcast.
	stalePending := store.Drain("stale")
	if len(stalePending) != 2 {
		t.Fatalf("stale pending = %d, want 2", len(stalePending))
	}
	if stalePending[1].Content != "tool installed" {
		t.Errorf("stale[1] = %q, want broadcast", stalePending[1].Content)
	}
}

func TestNotificationStore_UnsubscribeRemovesAndCloses(t *testing.T) {
	t.Parallel()

	store := notify.NewNotificationStore()
	ch := store.Subscribe(t.Context(), "S")

	// Push a value first so we can verify subsequent push goes to pending.
	store.Push(notify.Notification{SessionID: "S", Content: "1"})
	<-ch

	store.Unsubscribe("S", ch)

	// Channel must be closed.
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after Unsubscribe")
	}

	// Push without subscriber goes to pending.
	store.Push(notify.Notification{SessionID: "S", Content: "2"})
	pending := store.Drain("S")
	if len(pending) != 1 || pending[0].Content != "2" {
		t.Errorf("pending after unsubscribe = %+v", pending)
	}
}

func TestNotificationStore_SubscribeCtxAutoUnsubscribes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := notify.NewNotificationStore()
		ctx, cancel := context.WithCancel(t.Context())
		ch := store.Subscribe(ctx, "S")

		// Live: a push reaches the channel.
		store.Push(notify.Notification{SessionID: "S", Content: "live"})
		select {
		case n := <-ch:
			if n.Content != "live" {
				t.Errorf("got %q, want live", n.Content)
			}
		default:
			t.Fatal("live push not received")
		}

		// Cancel the ctx; AfterFunc fires the auto-unsubscribe.
		cancel()
		synctest.Wait()

		// Channel must be closed.
		if _, ok := <-ch; ok {
			t.Error("channel should be closed after ctx cancel")
		}

		// Subsequent push goes to pending — no live subscribers left.
		store.Push(notify.Notification{SessionID: "S", Content: "after"})
		pending := store.Drain("S")
		if len(pending) != 1 || pending[0].Content != "after" {
			t.Errorf("pending after auto-unsub = %+v, want one queued copy", pending)
		}
	})
}
