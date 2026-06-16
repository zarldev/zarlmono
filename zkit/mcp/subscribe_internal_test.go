package mcp

import (
	"encoding/json"
	"sync/atomic"
	"testing"
)

// These tests poke dispatchNotification directly to verify the
// fan-out semantics without standing up a transport. Subscribe and
// SubscribeAny are exposed; dispatchNotification is unexported but
// reachable from the same-package test, which is the cleanest way
// to drive the dispatcher deterministically.

func TestSubscribeRoutesByMethod(t *testing.T) {
	t.Parallel()
	c := &Client{}

	var aHits, bHits atomic.Int32
	cancelA := c.Subscribe("foo", func(json.RawMessage) { aHits.Add(1) })
	defer cancelA()
	cancelB := c.Subscribe("bar", func(json.RawMessage) { bHits.Add(1) })
	defer cancelB()

	c.dispatchNotification("foo", json.RawMessage(`{}`))
	c.dispatchNotification("foo", json.RawMessage(`{}`))
	c.dispatchNotification("bar", json.RawMessage(`{}`))
	c.dispatchNotification("nobody", json.RawMessage(`{}`))

	if got := aHits.Load(); got != 2 {
		t.Errorf("foo handler hits = %d, want 2", got)
	}
	if got := bHits.Load(); got != 1 {
		t.Errorf("bar handler hits = %d, want 1", got)
	}
}

func TestSubscribeAnyFiresForEveryMethod(t *testing.T) {
	t.Parallel()
	c := &Client{}

	var got []string
	cancel := c.SubscribeAny(func(method string, _ json.RawMessage) {
		got = append(got, method)
	})
	defer cancel()

	c.dispatchNotification("notifications/resources/updated", json.RawMessage(`{}`))
	c.dispatchNotification("custom.task.completed", json.RawMessage(`{}`))
	c.dispatchNotification("anything-at-all", json.RawMessage(`{}`))

	if len(got) != 3 {
		t.Fatalf("any-handler fired %d times, want 3: %v", len(got), got)
	}
	want := []string{"notifications/resources/updated", "custom.task.completed", "anything-at-all"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSubscribeAndSubscribeAnyBothFire(t *testing.T) {
	t.Parallel()
	c := &Client{}

	var specific, count atomic.Int32
	cancelS := c.Subscribe("foo", func(json.RawMessage) { specific.Add(1) })
	defer cancelS()
	cancelA := c.SubscribeAny(func(string, json.RawMessage) { count.Add(1) })
	defer cancelA()

	c.dispatchNotification("foo", json.RawMessage(`{}`))
	c.dispatchNotification("bar", json.RawMessage(`{}`))

	// Specific handler fires only for "foo"; the catch-all fires for both.
	if got := specific.Load(); got != 1 {
		t.Errorf("specific handler hits = %d, want 1", got)
	}
	if got := count.Load(); got != 2 {
		t.Errorf("any handler hits = %d, want 2", got)
	}
}

func TestSubscribeAnyCancelStopsFiring(t *testing.T) {
	t.Parallel()
	c := &Client{}

	var hits atomic.Int32
	cancel := c.SubscribeAny(func(string, json.RawMessage) { hits.Add(1) })

	c.dispatchNotification("a", json.RawMessage(`{}`))
	cancel()
	c.dispatchNotification("b", json.RawMessage(`{}`))
	c.dispatchNotification("c", json.RawMessage(`{}`))

	if got := hits.Load(); got != 1 {
		t.Errorf("hits after cancel = %d, want 1 (only the pre-cancel call)", got)
	}
}

func TestPayloadPassedThroughVerbatim(t *testing.T) {
	t.Parallel()
	c := &Client{}

	want := json.RawMessage(`{"task_id":42,"status":"done","result":[1,2,3]}`)
	var got json.RawMessage
	cancel := c.SubscribeAny(func(_ string, params json.RawMessage) {
		got = params
	})
	defer cancel()

	c.dispatchNotification("task.completed", want)

	if string(got) != string(want) {
		t.Errorf("payload mutated: got=%s want=%s", got, want)
	}
}
