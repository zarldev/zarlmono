package engine

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func collectQueue(q *queueState, t *testing.T) []llm.Message {
	t.Helper()
	return collectQueueWithContext(q, t.Context(), t)
}

func collectQueueWithContext(q *queueState, ctx context.Context, t *testing.T) []llm.Message {
	t.Helper()
	var out []llm.Message
	for msg := range q.Drain(ctx) {
		out = append(out, msg)
	}
	return out
}

func TestQueueStateAppendDrain(t *testing.T) {
	q := newQueueState()
	if got, _ := q.Append("   \n"); got != 0 {
		t.Fatalf("blank append depth = %d, want 0", got)
	}
	if got, _ := q.Append("first"); got != 1 {
		t.Fatalf("first append depth = %d, want 1", got)
	}
	if got, _ := q.Append("second"); got != 2 {
		t.Fatalf("second append depth = %d, want 2", got)
	}

	msgs := collectQueue(q, t)
	if len(msgs) != 2 {
		t.Fatalf("drained %d messages, want 2", len(msgs))
	}
	for i, want := range []string{"first", "second"} {
		if msgs[i].Role != "user" || msgs[i].Content != want {
			t.Fatalf("message %d = %+v, want user %q", i, msgs[i], want)
		}
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("queue len after drain = %d, want 0", got)
	}
	if msgs := collectQueue(q, t); len(msgs) != 0 {
		t.Fatalf("second drain returned %d messages, want 0", len(msgs))
	}
}

func TestQueueStateDrainIgnoresSubAgentDepth(t *testing.T) {
	q := newQueueState()
	q.Append("keep this for the parent")

	childCtx := taskscope.WithDepth(t.Context(), 1)
	if msgs := collectQueueWithContext(q, childCtx, t); len(msgs) != 0 {
		t.Fatalf("child drain returned %d messages, want 0", len(msgs))
	}
	if got := q.Len(); got != 1 {
		t.Fatalf("queue len after child drain = %d, want 1", got)
	}

	msgs := collectQueue(q, t)
	if len(msgs) != 1 || msgs[0].Content != "keep this for the parent" {
		t.Fatalf("parent drain = %+v, want queued message", msgs)
	}
}

func TestQueueStateSnapshot(t *testing.T) {
	q := newQueueState()
	q.Append("hello")
	q.Append("world")

	snapshot := q.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snapshot))
	}
	if snapshot[0].Message.Content != "hello" || snapshot[1].Message.Content != "world" {
		t.Fatalf("snapshot content = %q/%q", snapshot[0].Message.Content, snapshot[1].Message.Content)
	}
}

func TestQueueStateUpdate(t *testing.T) {
	q := newQueueState()
	_, id := q.Append("first")
	if !q.Update(id, "edited") {
		t.Fatal("Update returned false")
	}
	snapshot := q.Snapshot()
	if snapshot[0].Message.Content != "edited" {
		t.Fatalf("updated content = %q, want edited", snapshot[0].Message.Content)
	}
	if q.Update(9999, "missing") {
		t.Fatal("Update with unknown id should return false")
	}
}

func TestQueueStateRemove(t *testing.T) {
	q := newQueueState()
	q.Append("keep")
	_, id := q.Append("delete")
	if !q.Remove(id) {
		t.Fatal("Remove returned false")
	}
	snapshot := q.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Message.Content != "keep" {
		t.Fatalf("after remove: %+v", snapshot)
	}
	if q.Remove(9999) {
		t.Fatal("Remove on missing id should return false")
	}
}

func TestQueueStateClear(t *testing.T) {
	q := newQueueState()
	q.Append("a")
	q.Append("b")
	if n := q.Clear(); n != 2 {
		t.Fatalf("Clear returned %d, want 2", n)
	}
	if len(q.Snapshot()) != 0 {
		t.Fatal("queue not empty after Clear")
	}
}

func TestQueueStateAppendControl(t *testing.T) {
	q := newQueueState()
	_, id := q.AppendControl("stop after current tool")
	if id == 0 {
		t.Fatal("AppendControl returned zero id")
	}
	snapshot := q.Snapshot()
	if snapshot[0].Message.Content != "stop after current tool" {
		t.Fatalf("control content = %q", snapshot[0].Message.Content)
	}
}

func TestQueueStateAppendReturnsID(t *testing.T) {
	q := newQueueState()
	depth, id := q.Append("one")
	if depth != 1 || id == 0 {
		t.Fatalf("Append = (%d,%d), want (1, nonzero)", depth, id)
	}
	depth2, id2 := q.Append("two")
	if depth2 != 2 || id2 <= id {
		t.Fatalf("Append = (%d,%d), want (2, >%d)", depth2, id2, id)
	}
}
