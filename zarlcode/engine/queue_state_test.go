package engine_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func newQueueRunner(t *testing.T) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return engine.NewLiveRunner(nil, ws, "local")
}

func collectQueue(q *engine.LiveRunner, t *testing.T) []llm.Message {
	t.Helper()
	return collectQueueWithContext(q, t.Context(), t)
}

func collectQueueWithContext(q *engine.LiveRunner, ctx context.Context, t *testing.T) []llm.Message {
	t.Helper()
	var out []llm.Message
	for msg := range q.DrainQueue(ctx) {
		out = append(out, msg)
	}
	return out
}

func TestQueueStateAppendDrain(t *testing.T) {
	q := newQueueRunner(t)
	if got, _ := q.QueueAppend("   \n"); got != 0 {
		t.Fatalf("blank append depth = %d, want 0", got)
	}
	if got, _ := q.QueueAppend("first"); got != 1 {
		t.Fatalf("first append depth = %d, want 1", got)
	}
	if got, _ := q.QueueAppend("second"); got != 2 {
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
	if got := q.QueueLen(); got != 0 {
		t.Fatalf("queue len after drain = %d, want 0", got)
	}
	if msgs := collectQueue(q, t); len(msgs) != 0 {
		t.Fatalf("second drain returned %d messages, want 0", len(msgs))
	}
}

func TestQueueStateDrainIgnoresSubAgentDepth(t *testing.T) {
	q := newQueueRunner(t)
	q.QueueAppend("keep this for the parent")

	childCtx := taskscope.WithDepth(t.Context(), 1)
	if msgs := collectQueueWithContext(q, childCtx, t); len(msgs) != 0 {
		t.Fatalf("child drain returned %d messages, want 0", len(msgs))
	}
	if got := q.QueueLen(); got != 1 {
		t.Fatalf("queue len after child drain = %d, want 1", got)
	}

	msgs := collectQueue(q, t)
	if len(msgs) != 1 || msgs[0].Content != "keep this for the parent" {
		t.Fatalf("parent drain = %+v, want queued message", msgs)
	}
}

func TestQueueStateSnapshot(t *testing.T) {
	q := newQueueRunner(t)
	q.QueueAppend("hello")
	q.QueueAppend("world")

	snapshot := q.QueueSnapshot()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snapshot))
	}
	if snapshot[0].Message.Content != "hello" || snapshot[1].Message.Content != "world" {
		t.Fatalf("snapshot content = %q/%q", snapshot[0].Message.Content, snapshot[1].Message.Content)
	}
}

func TestQueueStateUpdate(t *testing.T) {
	q := newQueueRunner(t)
	_, id := q.QueueAppend("first")
	if !q.QueueUpdate(id, "edited") {
		t.Fatal("Update returned false")
	}
	snapshot := q.QueueSnapshot()
	if snapshot[0].Message.Content != "edited" {
		t.Fatalf("updated content = %q, want edited", snapshot[0].Message.Content)
	}
	if q.QueueUpdate(9999, "missing") {
		t.Fatal("Update with unknown id should return false")
	}
}

func TestQueueStateRemove(t *testing.T) {
	q := newQueueRunner(t)
	q.QueueAppend("keep")
	_, id := q.QueueAppend("delete")
	if !q.QueueRemove(id) {
		t.Fatal("Remove returned false")
	}
	snapshot := q.QueueSnapshot()
	if len(snapshot) != 1 || snapshot[0].Message.Content != "keep" {
		t.Fatalf("after remove: %+v", snapshot)
	}
	if q.QueueRemove(9999) {
		t.Fatal("Remove on missing id should return false")
	}
}

func TestQueueStateClear(t *testing.T) {
	q := newQueueRunner(t)
	q.QueueAppend("a")
	q.QueueAppend("b")
	if n := q.QueueClear(); n != 2 {
		t.Fatalf("Clear returned %d, want 2", n)
	}
	if len(q.QueueSnapshot()) != 0 {
		t.Fatal("queue not empty after Clear")
	}
}

func TestQueueStateAppendControl(t *testing.T) {
	q := newQueueRunner(t)
	_, id := q.QueueAppendControl("stop after current tool")
	if id == 0 {
		t.Fatal("AppendControl returned zero id")
	}
	snapshot := q.QueueSnapshot()
	if snapshot[0].Message.Content != "stop after current tool" {
		t.Fatalf("control content = %q", snapshot[0].Message.Content)
	}
}

func TestQueueStateAppendReturnsID(t *testing.T) {
	q := newQueueRunner(t)
	depth, id := q.QueueAppend("one")
	if depth != 1 || id == 0 {
		t.Fatalf("Append = (%d,%d), want (1, nonzero)", depth, id)
	}
	depth2, id2 := q.QueueAppend("two")
	if depth2 != 2 || id2 <= id {
		t.Fatalf("Append = (%d,%d), want (2, >%d)", depth2, id2, id)
	}
}
