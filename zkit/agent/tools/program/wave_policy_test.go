package program_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestCallManyWavesKeepBudgetsAndWholeBatchPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		executions   int32
		kind         tools.Kind
	}{
		{"remaining budget", `call("echo"); emit(call_many([{"name": "echo"} for i in range(3)]))`, 1, tools.Kinds.BUDGET},
		{"total batch budget", `emit(call_many([{"name": "echo"} for i in range(4)]))`, 0, tools.Kinds.BUDGET},
		{"denied last entry", `emit(call_many([{"name": "echo"}, {"name": "echo"}, {"name": "denied"}]))`, 0, tools.Kinds.PERMISSION},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var executions atomic.Int32
			fn := func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
				executions.Add(1)
				return tools.Success(call.ID, "ok"), nil
			}
			inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
				"echo":   {spec: tools.ToolSpec{Name: "echo"}, fn: fn},
				"denied": {spec: tools.ToolSpec{Name: "denied"}, fn: fn},
			}}
			src, err := program.NewSource(inner, program.WithPolicy(func(spec tools.ToolSpec) bool { return spec.Name == "echo" }), program.WithLimits(program.Limits{MaxToolCalls: 3, MaxParallelCalls: 1}))
			if err != nil {
				t.Fatal(err)
			}
			res, err := src.Execute(t.Context(), tools.ToolCall{ID: "budget", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": tc.script}})
			if err != nil {
				t.Fatal(err)
			}
			if res.Success || res.Err == nil || res.Err.Kind != tc.kind || executions.Load() != tc.executions {
				t.Fatalf("result=%+v executions=%d, want %d kind=%s", res, executions.Load(), tc.executions, tc.kind)
			}
		})
	}
}
