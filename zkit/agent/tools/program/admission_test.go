package program_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestProgramAdmissionReferencesFollowEmittedResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   int
	}{
		{"failure emitted", `emit(call("failed"))`, 1},
		{"failure discarded", `call("failed"); emit("other work")`, 0},
		{"failure transformed", `r = call("failed"); r["error"] = "removed"; emit(r)`, 0},
		{"failure reconstructed", `r = call("failed"); emit({"error": r["error"]})`, 0},
		{"failure data only", `r = call("failed"); emit(r["data"])`, 0},
		{"failure reconstructed data", `r = call("failed"); emit({"data":r["data"]})`, 0},
		{"mixed parallel emitted", `emit(call_many([{"name":"failed"}, {"name":"succeeded"}]))`, 2},
		{"mixed parallel filtered", `r = call_many([{"name":"failed"}, {"name":"succeeded"}]); emit(r[1])`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeSource{tools: map[tools.ToolName]fakeTool{}}
			for _, name := range []tools.ToolName{"failed", "succeeded"} {
				inner.tools[name] = fakeTool{spec: tools.ToolSpec{Name: name}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
					result := tools.Success(call.ID, map[string]any{"summary": "complete evidence"})
					if name == "failed" {
						result = tools.Failure(call.ID, tools.Transient("child", context.DeadlineExceeded))
						result.Data = map[string]any{"summary": "partial evidence"}
					}
					result.AdmissionReferences = []tools.AdmissionReference{{Namespace: "completion", ID: string(name)}}
					return result, nil
				}}
			}
			source, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
			if err != nil {
				t.Fatal(err)
			}
			result, err := source.Execute(t.Context(), tools.ToolCall{ID: "outer", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": tc.script}})
			if err != nil || !result.Success {
				t.Fatalf("program = %#v, %v", result, err)
			}
			if len(result.AdmissionReferences) != tc.want {
				t.Fatalf("references = %#v, want %d", result.AdmissionReferences, tc.want)
			}
		})
	}
}
