package program_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	program "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type fakeTool struct {
	spec tools.ToolSpec
	fn   func(context.Context, tools.ToolCall) (*tools.ToolResult, error)
}

func (f fakeTool) Definition() tools.ToolSpec { return f.spec }
func (f fakeTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return f.fn(ctx, call)
}

type fakeSource struct{ tools map[tools.ToolName]fakeTool }

func (s *fakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		for _, tool := range s.tools {
			if !yield(tool) {
				return
			}
		}
	}
}

func (s *fakeSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	tool, ok := s.tools[call.ToolName]
	if !ok {
		return tools.Failure(call.ID, tools.NotFound("fake", "missing")), nil
	}
	return tool.Execute(ctx, call)
}

func TestProgramCallsAndEmitsFilteredResult(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"echo": {spec: tools.ToolSpec{Name: "echo"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			return tools.Success(call.ID, map[string]any{"value": call.Arguments["value"], "hidden": "large intermediate"}), nil
		}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(spec tools.ToolSpec) bool { return spec.Name == "echo" }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := src.Execute(context.Background(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `
r = call("echo", {"value": "kept"})
emit({"ok": r["ok"], "value": r["data"]["value"]})
`}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("program failed: %v", res.Err)
	}
	got := res.Data.(program.Result).Output.(map[string]any)
	if got["value"] != "kept" || got["ok"] != true {
		t.Fatalf("output = %#v", got)
	}
	if _, ok := got["hidden"]; ok {
		t.Fatalf("unfiltered intermediate leaked: %#v", got)
	}
}

type recordingNestedObserver struct {
	mu       sync.Mutex
	started  []tools.NestedToolCall
	finished []tools.NestedToolResult
}

func (o *recordingNestedObserver) OnNestedToolStarted(_ context.Context, e tools.NestedToolCall) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, e)
}

func (o *recordingNestedObserver) OnNestedToolFinished(_ context.Context, e tools.NestedToolResult) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.finished = append(o.finished, e)
}

func TestProgramEmitsNestedObserverEventsInDeclarationOrder(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"echo": {spec: tools.ToolSpec{Name: "echo"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			return tools.Success(call.ID, map[string]any{"value": call.Arguments["value"]}), nil
		}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(spec tools.ToolSpec) bool { return spec.Name == "echo" }))
	if err != nil {
		t.Fatal(err)
	}
	obs := &recordingNestedObserver{}
	ctx := tools.ContextWithNestedToolObserver(context.Background(), obs)
	res, err := src.Execute(ctx, tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `
rs = call_many([
  {"name": "echo", "args": {"value": "a"}},
  {"name": "echo", "args": {"value": "b"}},
])
emit(rs)
`}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("program failed: %v", res.Err)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.started) != 2 || len(obs.finished) != 2 {
		t.Fatalf("events = %d started/%d finished, want 2/2", len(obs.started), len(obs.finished))
	}
	started := map[int]tools.NestedToolCall{}
	finished := map[int]tools.NestedToolResult{}
	for _, e := range obs.started {
		started[e.Sequence] = e
	}
	for _, e := range obs.finished {
		finished[e.Sequence] = e
	}
	for i := range 2 {
		wantID := tools.ToolCallID("outer/" + string(rune('0'+i)))
		if started[i].ChildID != wantID {
			t.Fatalf("started seq %d = id %q, want %q", i, started[i].ChildID, wantID)
		}
		if finished[i].ChildID != wantID || finished[i].Result == nil || !finished[i].Result.Success {
			t.Fatalf("finished seq %d = %#v", i, finished[i])
		}
	}
}

func TestToolsHidesOnlyProgramPolicyTools(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"read":  {spec: tools.ToolSpec{Name: "read"}},
		"write": {spec: tools.ToolSpec{Name: "write", Mutates: true}},
		"bash":  {spec: tools.ToolSpec{Name: "bash", AffectsWorkspace: true}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(spec tools.ToolSpec) bool { return spec.Name == "read" }))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[tools.ToolName]bool{}
	for tool := range src.Tools(context.Background()) {
		seen[tool.Definition().Name] = true
	}
	if !seen[program.ToolName] || !seen["write"] || !seen["bash"] {
		t.Fatalf("program/write/bash should remain visible: %v", seen)
	}
	if seen["read"] {
		t.Fatalf("policy-hidden read tool leaked: %v", seen)
	}
}

func TestDenyByDefaultAndNoEmitFail(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{"echo": {spec: tools.ToolSpec{Name: "echo"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
		return tools.Success(call.ID, nil), nil
	}}}}
	src, err := program.NewSource(inner)
	if err != nil {
		t.Fatal(err)
	}
	res, err := src.Execute(context.Background(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `emit(call("echo", {}))`}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Err == nil || res.Err.Kind != tools.Kinds.PERMISSION {
		t.Fatalf("denied result = %#v", res)
	}

	src, err = program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	res, err = src.Execute(context.Background(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `x = 1`}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Err == nil || res.Err.Kind != tools.Kinds.VALIDATION {
		t.Fatalf("no emit result = %#v", res)
	}
}

func TestCallManyPreservesOrderAndLimitsConcurrency(t *testing.T) {
	var mu sync.Mutex
	active, peak := 0, 0
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"echo": {spec: tools.ToolSpec{Name: "echo"}, fn: func(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			select {
			case <-time.After(20 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			mu.Lock()
			active--
			mu.Unlock()
			return tools.Success(call.ID, call.Arguments["value"]), nil
		}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(spec tools.ToolSpec) bool { return spec.Name == "echo" }), program.WithLimits(program.Limits{MaxToolCalls: 4, MaxParallelCalls: 2, Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := src.Execute(context.Background(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `
rs = call_many([
  {"name": "echo", "args": {"value": "a"}},
  {"name": "echo", "args": {"value": "b"}},
])
emit([r["data"] for r in rs])
`}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("program failed: %v", res.Err)
	}
	out := res.Data.(program.Result).Output.([]any)
	if out[0] != "a" || out[1] != "b" {
		t.Fatalf("output order = %#v", out)
	}
	if peak > 2 {
		t.Fatalf("peak concurrency = %d", peak)
	}
}

func TestResultStringRendersOnlyOutput(t *testing.T) {
	got := program.Result{Output: map[string]any{"answer": "yes"}, Stats: program.Stats{ToolCalls: 2}}.String()
	if strings.Contains(got, "Stats") || strings.Contains(got, "ToolCalls") || strings.Contains(got, "Output") {
		t.Fatalf("wrapper leaked into result string: %s", got)
	}
	if !strings.Contains(got, "answer") || !strings.Contains(got, "yes") {
		t.Fatalf("output missing from result string: %s", got)
	}
}

func TestProgramNormalizesTypedToolResults(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"typed": {spec: tools.ToolSpec{Name: "typed"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			return tools.Success(call.ID, typedPayload{Name: "ok"}), nil
		}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(spec tools.ToolSpec) bool { return spec.Name == "typed" }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := src.Execute(context.Background(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `emit(call("typed", {})["data"])`}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("program failed: %v", res.Err)
	}
	out := res.Data.(program.Result).Output.(map[string]any)
	if out["name"] != "ok" {
		t.Fatalf("normalized typed output = %#v", out)
	}
}

type typedPayload struct {
	Name string `json:"name"`
}

func TestConstructorValidation(t *testing.T) {
	if _, err := program.NewSource(nil); err == nil {
		t.Fatal("program.NewSource(nil) succeeded")
	}
	inner := &fakeSource{}
	if _, err := program.NewSource(inner, program.WithLimits(program.Limits{MaxToolCalls: 1, MaxParallelCalls: 2})); err == nil {
		t.Fatal("invalid limits succeeded")
	}
}
