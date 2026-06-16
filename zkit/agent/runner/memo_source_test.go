package runner_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// countingSource counts every Execute and returns a configurable
// result. Lets the memo tests assert dispatch-count semantics.
type countingSource struct {
	calls  int
	result *tools.ToolResult
	err    error
}

func (c *countingSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	_ = ctx
	return func(yield func(tools.Tool) bool) {}
}

func (c *countingSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	c.calls++
	if c.result == nil {
		return nil, c.err
	}
	r := *c.result
	r.ToolCallID = call.ID
	return &r, c.err
}

// Task-scoped memoization defaults to the empty taskscope.ID when the
// context is not inside a Runner.Run dispatch. That path is still useful:
// any consumer that wraps a MemoSource without going through Runner.Run gets
// the "no task" bucket, which is still memoized per-process. The runner-driven
// path is covered by an end-to-end test below.

func TestMemoSource_PassThroughWhenImpure(t *testing.T) {
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "x"}}
	m := runner.NewMemoSource(inner, runner.PureTools("read")) // bash NOT pure

	call := tools.ToolCall{ID: "1", ToolName: "bash", Arguments: tools.ToolParameters{"command": "ls"}}
	_, _ = m.Execute(context.Background(), call)
	call.ID = "2"
	_, _ = m.Execute(context.Background(), call)

	if inner.calls != 2 {
		t.Errorf("impure tool: dispatch count = %d, want 2 (no caching)", inner.calls)
	}
}

func TestMemoSource_CachesPureToolWithinTask(t *testing.T) {
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "file contents"}}
	m := runner.NewMemoSource(inner, runner.PureTools("read"))

	call := tools.ToolCall{ID: "1", ToolName: "read", Arguments: tools.ToolParameters{"path": "foo.go"}}
	r1, _ := m.Execute(context.Background(), call)
	call.ID = "2"
	r2, _ := m.Execute(context.Background(), call)

	if inner.calls != 1 {
		t.Errorf("pure tool: dispatch count = %d, want 1 (second call should hit cache)", inner.calls)
	}
	if r1.Data != r2.Data {
		t.Errorf("cached payload differs: %v vs %v", r1.Data, r2.Data)
	}
	if r2.ToolCallID != "2" {
		t.Errorf("cached result kept old call ID %q; want %q (clone should re-stamp)", r2.ToolCallID, "2")
	}
}

// Regression: a model that's been nudged toward calling list_skills /
// list_agents / ls every turn used to compound — the cache returned
// silently and the model never saw it was looping (32-tool-call
// turns with 25 duplicates). The third identical call must now
// return a Validation rejection so the model gets explicit feedback.
func TestMemoSource_LoopRejectedOnThirdIdenticalCall(t *testing.T) {
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "skill list"}}
	m := runner.NewMemoSource(inner, runner.PureTools("list_skills"))

	call := tools.ToolCall{ID: "1", ToolName: "list_skills", Arguments: tools.ToolParameters{}}
	// 1st call: dispatch (cache miss), success returned.
	r1, _ := m.Execute(context.Background(), call)
	if r1 == nil || !r1.Success {
		t.Fatalf("first call: want success, got %+v", r1)
	}

	// 2nd call: first cache hit — still silent, model might legitimately
	// have re-checked. Successful result, no error.
	call.ID = "2"
	r2, _ := m.Execute(context.Background(), call)
	if r2 == nil || !r2.Success {
		t.Fatalf("second call (first repeat): want silent cached success, got %+v", r2)
	}
	if r2.Data != "skill list" {
		t.Errorf("second call: cached payload differs: %v", r2.Data)
	}

	// 3rd call: second repeat — the loop signal. Validation rejection
	// pointing at the prior result so the model stops looping.
	call.ID = "3"
	r3, _ := m.Execute(context.Background(), call)
	if r3 == nil || r3.Success {
		t.Fatalf("third call (second repeat): want Validation rejection, got %+v", r3)
	}
	if r3.Err == nil || r3.Err.Kind != tools.Kinds.VALIDATION {
		t.Errorf("third call: Err.Kind = %v, want Validation", r3.Err)
	}
	if !strings.Contains(r3.Error, "duplicate call") {
		t.Errorf("third call: Error missing 'duplicate call' nudge: %q", r3.Error)
	}
	if r3.ToolCallID != "3" {
		t.Errorf("third call: ToolCallID = %q, want %q", r3.ToolCallID, "3")
	}

	// The inner tool was only ever dispatched once — the cache spared
	// the dispatch on every repeat regardless of the rejection.
	if inner.calls != 1 {
		t.Errorf("inner dispatches = %d, want 1 (cache must spare every repeat)", inner.calls)
	}
}

func TestMemoSource_DifferentArgsDifferentBuckets(t *testing.T) {
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "x"}}
	m := runner.NewMemoSource(inner, runner.PureTools("read"))

	call1 := tools.ToolCall{ID: "1", ToolName: "read", Arguments: tools.ToolParameters{"path": "foo.go"}}
	call2 := tools.ToolCall{ID: "2", ToolName: "read", Arguments: tools.ToolParameters{"path": "bar.go"}}
	_, _ = m.Execute(context.Background(), call1)
	_, _ = m.Execute(context.Background(), call2)

	if inner.calls != 2 {
		t.Errorf("different args: dispatch count = %d, want 2", inner.calls)
	}
}

func TestMemoSource_DoesNotCacheFailures(t *testing.T) {
	failing := &countingSource{result: &tools.ToolResult{Success: false, Error: "transient"}}
	m := runner.NewMemoSource(failing, runner.PureTools("read"))

	call := tools.ToolCall{ID: "1", ToolName: "read", Arguments: tools.ToolParameters{"path": "x"}}
	_, _ = m.Execute(context.Background(), call)
	call.ID = "2"
	_, _ = m.Execute(context.Background(), call)

	if failing.calls != 2 {
		t.Errorf("failed result: dispatch count = %d, want 2 (failures shouldn't be cached)", failing.calls)
	}
}

func TestMemoSource_DoesNotCacheExecErrors(t *testing.T) {
	erroring := &countingSource{err: errors.New("boom")}
	m := runner.NewMemoSource(erroring, runner.PureTools("read"))

	call := tools.ToolCall{ID: "1", ToolName: "read"}
	_, _ = m.Execute(context.Background(), call)
	_, _ = m.Execute(context.Background(), call)

	if erroring.calls != 2 {
		t.Errorf("exec error: dispatch count = %d, want 2 (errors shouldn't be cached)", erroring.calls)
	}
}

func TestMemoSource_NilPureFnDisables(t *testing.T) {
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "x"}}
	m := runner.NewMemoSource(inner, nil)

	call := tools.ToolCall{ID: "1", ToolName: "read"}
	_, _ = m.Execute(context.Background(), call)
	_, _ = m.Execute(context.Background(), call)

	if inner.calls != 2 {
		t.Errorf("nil PureFn: dispatch count = %d, want 2 (memoization disabled)", inner.calls)
	}
}

func TestMemoSource_ArgumentOrderingDoesNotMatter(t *testing.T) {
	// Maps with the same content but different insertion order should
	// hit the same cache slot.
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "x"}}
	m := runner.NewMemoSource(inner, runner.PureTools("read"))

	a := tools.ToolCall{ID: "1", ToolName: "read", Arguments: tools.ToolParameters{"a": 1, "b": 2}}
	b := tools.ToolCall{ID: "2", ToolName: "read", Arguments: tools.ToolParameters{"b": 2, "a": 1}}
	_, _ = m.Execute(context.Background(), a)
	_, _ = m.Execute(context.Background(), b)

	if inner.calls != 1 {
		t.Errorf("equivalent args in different order: dispatch count = %d, want 1", inner.calls)
	}
}

func TestMemoSource_ForgetTask(t *testing.T) {
	inner := &countingSource{result: &tools.ToolResult{Success: true, Data: "x"}}
	m := runner.NewMemoSource(inner, runner.PureTools("read"))

	call := tools.ToolCall{ID: "1", ToolName: "read", Arguments: tools.ToolParameters{"path": "x"}}
	_, _ = m.Execute(context.Background(), call)
	// ctx has no TaskID — that "no task" bucket is what got populated.
	m.ForgetTask("")
	_, _ = m.Execute(context.Background(), call)

	if inner.calls != 2 {
		t.Errorf("after ForgetTask: dispatch count = %d, want 2", inner.calls)
	}
}
