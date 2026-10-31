package program_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestProgramCompactReadPreservesErrorsAndPartialData(t *testing.T) {
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{
		"read": {spec: tools.ToolSpec{Name: "read"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
			result := tools.Failure(call.ID, tools.NotFound("read", "missing source"))
			result.Data = "partial source; consult recovery path"
			return result, nil
		}},
	}}
	src, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := src.Execute(t.Context(), tools.ToolCall{ID: "example", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `
r = call("read", {"path": "main.go", "limit": 80})
emit("main.go\n" + r["data"] if r["ok"] else r)`}})
	if err != nil || !res.Success {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	out := res.Data.(program.Result).Output.(map[string]any)
	if out["ok"] != false || out["error"] == "" || out["data"] != "partial source; consult recovery path" {
		t.Fatalf("lost failure/partial data: %#v", out)
	}
}
