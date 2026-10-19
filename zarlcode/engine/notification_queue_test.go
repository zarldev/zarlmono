package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	maxQueuedMCPNotifications = 32
	maxQueuedMCPBytes         = 32 * 1024
)

func TestMCPNotificationQueueBoundsIdleBursts(t *testing.T) {
	t.Run("message count", func(t *testing.T) {
		live := newQueueRunner(t)
		injector := live.QueueInjector()
		for range 100 {
			injector.Append("remote event")
		}

		snapshot := live.QueueSnapshot()
		if len(snapshot) != maxQueuedMCPNotifications {
			t.Fatalf("queued notifications = %d, want %d", len(snapshot), maxQueuedMCPNotifications)
		}
		assertRemoteNotifications(t, snapshot)
	})

	t.Run("bytes", func(t *testing.T) {
		live := newQueueRunner(t)
		injector := live.QueueInjector()
		payload := strings.Repeat("x", 8*1024)
		for range 100 {
			injector.Append(payload)
		}

		snapshot := live.QueueSnapshot()
		assertRemoteNotifications(t, snapshot)
		queuedBytes := 0
		for _, queued := range snapshot {
			queuedBytes += len(queued.Message.Content)
		}
		if queuedBytes != maxQueuedMCPBytes {
			t.Fatalf("queued notification bytes = %d, want %d", queuedBytes, maxQueuedMCPBytes)
		}
		if queuedBytes+len(payload) <= maxQueuedMCPBytes {
			t.Fatalf("queue stopped below byte budget: queued=%d next=%d budget=%d", queuedBytes, len(payload), maxQueuedMCPBytes)
		}
	})
}

func TestMCPNotificationBudgetDoesNotDisplaceHumanInput(t *testing.T) {
	live := newQueueRunner(t)
	if _, id := live.QueueAppend("human before burst"); id == 0 {
		t.Fatal("initial human input was not queued")
	}
	injector := live.QueueInjector()
	for range maxQueuedMCPNotifications + 10 {
		injector.Append("remote event")
	}
	if _, id := live.QueueAppend("human after full notification queue"); id == 0 {
		t.Fatal("human input was rejected by a full notification queue")
	}
	injector.Append("rejected remote event")

	snapshot := live.QueueSnapshot()
	if len(snapshot) != maxQueuedMCPNotifications+2 {
		t.Fatalf("total queue length = %d, want %d", len(snapshot), maxQueuedMCPNotifications+2)
	}
	if snapshot[0].Message.Content != "human before burst" || snapshot[len(snapshot)-1].Message.Content != "human after full notification queue" {
		t.Fatalf("human inputs were displaced: first=%q last=%q", snapshot[0].Message.Content, snapshot[len(snapshot)-1].Message.Content)
	}
	if snapshot[0].Message.Observation.ID != "" || snapshot[len(snapshot)-1].Message.Observation.ID != "" {
		t.Fatal("human input was classified as a remote notification")
	}
	assertRemoteNotifications(t, snapshot[1:len(snapshot)-1])
}

func TestMCPNotificationBudgetResetsAfterDrain(t *testing.T) {
	live := newQueueRunner(t)
	injector := live.QueueInjector()
	for range maxQueuedMCPNotifications + 1 {
		injector.Append("first burst")
	}
	first := collectQueue(live, t)
	if len(first) != maxQueuedMCPNotifications {
		t.Fatalf("first drained burst = %d, want %d", len(first), maxQueuedMCPNotifications)
	}
	if live.QueueLen() != 0 {
		t.Fatal("queue remained populated after drain")
	}

	for range maxQueuedMCPNotifications + 1 {
		injector.Append("second burst")
	}
	second := live.QueueSnapshot()
	if len(second) != maxQueuedMCPNotifications {
		t.Fatalf("second queued burst = %d, want %d", len(second), maxQueuedMCPNotifications)
	}
	assertRemoteNotifications(t, second)
}

func TestMCPNotificationAdmissionIsBlockedByReservationAndShutdown(t *testing.T) {
	live := newQueueRunner(t)
	injector := live.QueueInjector()

	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatalf("reserve runtime: %v", err)
	}
	if depth := injector.Append("during reservation"); depth != 0 {
		t.Fatalf("reserved append depth = %d, want 0", depth)
	}
	if live.QueueLen() != 0 {
		t.Fatal("notification mutated queue during reservation")
	}
	reservation.Release()

	if depth := injector.Append("after reservation"); depth != 1 {
		t.Fatalf("released append depth = %d, want 1", depth)
	}
	live.QueueClear()
	if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("close live runner: %v", err)
	}
	if depth := injector.Append("after shutdown"); depth != 0 {
		t.Fatalf("shutdown append depth = %d, want 0", depth)
	}
	if live.QueueLen() != 0 {
		t.Fatal("notification mutated queue after shutdown")
	}
}

func assertRemoteNotifications(t *testing.T, queued []engine.QueuedMessage) {
	t.Helper()
	for i, item := range queued {
		if item.Message.Role != llm.RoleUser {
			t.Fatalf("notification %d role = %q, want %q", i, item.Message.Role, llm.RoleUser)
		}
		if !strings.HasPrefix(item.Message.Observation.ID, "mcp-notification:") {
			t.Fatalf("notification %d observation id = %q", i, item.Message.Observation.ID)
		}
	}
}
