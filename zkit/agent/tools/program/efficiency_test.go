package program_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestCallManyRunsBoundedWaves(t *testing.T) {
	for _, cancelQueued := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelQueued), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var active, peak, executions atomic.Int32
				inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
					"echo": {spec: tools.ToolSpec{Name: "echo"}, fn: func(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
						n := active.Add(1)
						defer active.Add(-1)
						executions.Add(1)
						for old := peak.Load(); n > old; old = peak.Load() {
							if peak.CompareAndSwap(old, n) {
								break
							}
						}
						if cancelQueued {
							cancel()
						}
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-time.After(time.Second):
						}
						return tools.Success(call.ID, call.Arguments["value"]), nil
					}},
				}}
				src, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }),
					program.WithLimits(program.Limits{MaxToolCalls: 5, MaxParallelCalls: 2}))
				if err != nil {
					t.Fatal(err)
				}
				observer := &recordingNestedObserver{}
				ctx = tools.ContextWithNestedToolObserver(ctx, observer)
				res, err := src.Execute(ctx, tools.ToolCall{ID: "waves", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `
rs = call_many([{"name": "echo", "args": {"value": i}} for i in range(5)])
emit([r["data"] for r in rs])`}})
				if err != nil {
					t.Fatal(err)
				}
				observer.mu.Lock()
				settled := len(observer.finished)
				observer.mu.Unlock()
				if settled != 5 {
					t.Fatalf("settled children=%d, want 5", settled)
				}
				if active.Load() != 0 || peak.Load() > 2 {
					t.Fatalf("active=%d peak=%d", active.Load(), peak.Load())
				}
				if cancelQueued {
					if res.Success || executions.Load() > 2 {
						t.Fatalf("queued cancellation: result=%+v executions=%d", res, executions.Load())
					}
					return
				}
				if !res.Success {
					t.Fatalf("waves: %v", res.Err)
				}
				out := res.Data.(program.Result).Output.([]any)
				if len(out) != 5 || executions.Load() != 5 || peak.Load() != 2 {
					t.Fatalf("output=%v executions=%d peak=%d", out, executions.Load(), peak.Load())
				}
				for i, value := range out {
					if value != float64(i) {
						t.Fatalf("result[%d]=%#v", i, value)
					}
				}
			})
		})
	}
}

func TestProgramSignatureEnumsDefaultsAndReadLimits(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"search": {spec: tools.ToolSpec{Name: "search", Parameters: llm.Schema{Properties: map[string]llm.Schema{
			"output":      {Type: "string", Enum: []any{"labeled", "json"}, Extra: map[string]any{"default": "labeled"}},
			"retries":     {Type: "integer", Enum: []any{1, 2}},
			"enabled":     {Type: "boolean", Enum: []any{false, true}},
			"max_results": {Type: "integer", Extra: map[string]any{"default": 100}},
			"offset":      {Type: "integer", Description: "Zero-based line offset."},
			"pattern":     {Type: "string"},
		}, Required: []string{"pattern"}}}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	for tool := range src.Tools(t.Context()) {
		description := tool.Definition().Description
		for _, want := range []string{`output?: ["labeled","json"] = "labeled"`, `retries?: [1,2]`, `enabled?: [false,true]`, `pattern: string`, `max_results?: integer = 100`, `offset?: integer (Zero-based line offset.)`} {
			if !strings.Contains(description, want) {
				t.Errorf("missing %q in %s", want, description)
			}
		}
	}
}

func TestProgramCompactExamplesPreserveUsefulData(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"read": {spec: tools.ToolSpec{Name: "read"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			return tools.Success(call.ID, "1:abc|package main\n[truncated; continue at offset=80]\nApplicable guidance: AGENTS.md"), nil
		}},
		"grep": {spec: tools.ToolSpec{Name: "grep"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			return tools.Success(call.ID, code.GrepResult{Hits: []code.GrepHit{{File: "main.go", Line: 3, Text: "// TODO"}}, Truncated: true, Pattern: "TODO"}), nil
		}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	var description string
	for tool := range src.Tools(t.Context()) {
		description = tool.Definition().Description
	}
	readStart := strings.Index(description, "Read example")
	readLines := strings.Split(description[readStart:], "\n")
	for _, example := range []struct{ marker, script, want string }{
		{"Read example", strings.TrimSpace(readLines[1]) + "\n" + strings.TrimSpace(readLines[2]), "main.go\n1:abc|package main\n[truncated; continue at offset=80]\nApplicable guidance: AGENTS.md"},
		{"Grep example", `r = call("grep", {"pattern": "TODO", "max_results": 20}); emit({"hits": r["data"]["Hits"], "truncated": r["data"]["Truncated"]} if r["ok"] else r)`, `{"hits":[{"file":"main.go","line":3,"text":"// TODO"}],"truncated":true}`},
	} {
		script := example.script
		res, err := src.Execute(t.Context(), tools.ToolCall{ID: "example", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": script}})
		if err != nil || !res.Success {
			t.Fatalf("example %s: result=%+v err=%v", example.marker, res, err)
		}
		if got := res.Data.(program.Result).String(); got != example.want {
			t.Fatalf("%s output=%q", example.marker, got)
		}
	}
}

func BenchmarkProgramOutputProjection(b *testing.B) {
	payload := strings.Repeat("1:abc|source line with anchors\n", 80)
	for _, project := range []bool{false, true} {
		b.Run(fmt.Sprintf("project=%t", project), func(b *testing.B) {
			inner := &fakeSource{tools: map[tools.ToolName]fakeTool{"read": {spec: tools.ToolSpec{Name: "read"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
				return tools.Success(call.ID, payload), nil
			}}}}
			src, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
			if err != nil {
				b.Fatal(err)
			}
			script := `r = call("read", {"path": "main.go", "limit": 80}); emit(r)`
			if project {
				script = `r = call("read", {"path": "main.go", "limit": 80}); emit("main.go\n" + r["data"] if r["ok"] else r)`
			}
			b.ReportAllocs()
			for b.Loop() {
				res, err := src.Execute(b.Context(), tools.ToolCall{ID: "benchmark", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": script}})
				if err != nil || !res.Success {
					b.Fatalf("result=%+v err=%v", res, err)
				}
				b.ReportMetric(float64(len(res.Data.(program.Result).String())), "output-bytes/op")
			}
		})
	}
}
