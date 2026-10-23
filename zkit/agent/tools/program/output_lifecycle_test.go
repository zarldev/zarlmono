package program_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestOutputStoreRevokesCapturedSourceBeforeDrain(t *testing.T) {
	old := program.NewOutputStore()
	t.Cleanup(old.Close)
	receipt, err := old.Retain(t.Context(), []byte(`"old"`))
	if err != nil {
		t.Fatal(err)
	}
	reader := program.NewOutputReadTool(old)
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	inner := &fakeSource{tools: map[tools.ToolName]fakeTool{"read": {spec: tools.ToolSpec{Name: "read"}, fn: func(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
		close(started)
		<-release
		return tools.Success(call.ID, strings.Repeat("x", 70000)), nil
	}}}}
	source, err := program.NewSource(inner, program.WithPolicy(func(tools.ToolSpec) bool { return true }), program.WithOutputRetainer(old))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var result *tools.ToolResult
	var executionErr error
	go func() {
		defer close(done)
		result, executionErr = source.Execute(t.Context(), tools.ToolCall{ID: "late", ToolName: program.ToolName, Arguments: tools.ToolParameters{"script": `emit(call("read"))`}})
	}()
	defer func() { unblock(); <-done }()
	<-started
	old.Close()
	fresh := program.NewOutputStore()
	t.Cleanup(fresh.Close)
	page, err := reader.Execute(t.Context(), tools.ToolCall{ID: "old", Arguments: tools.ToolParameters{"receipt": receipt}})
	if err != nil || page.Success {
		t.Fatalf("stale reader: %+v, %v", page, err)
	}
	assertOutputErrorKind(t, page.Err, tools.Kinds.NOTFOUND)
	if _, err := fresh.Read(t.Context(), program.OutputReadArgs{Receipt: receipt}); err == nil {
		t.Fatal("receipt crossed activation")
	}
	// The source still owns the old capability, not the replacement store.
	unblock()
	<-done
	if executionErr != nil || result.Success || strings.Contains(result.Error, "receipt=") || len(result.AdmissionReferences) != 0 {
		t.Fatalf("late emission: %+v, %v", result, executionErr)
	}
	assertOutputErrorKind(t, result.Err, tools.Kinds.BUDGET)
}
