package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolCallReportOutputIsInvocationOnly(t *testing.T) {
	called := false
	call := tools.ToolCall{
		ID: "call", ToolName: "test",
		ReportOutput: func(context.Context, tools.ToolOutputUpdate) { called = true },
	}
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "ReportOutput") || strings.Contains(string(encoded), "report_output") {
		t.Fatalf("invocation callback serialized: %s", encoded)
	}
	call.ReportOutput(t.Context(), tools.ToolOutputUpdate{Text: "preview"})
	if !called {
		t.Fatal("invocation callback was not callable")
	}
	if tools.MaxToolOutputPreviewBytes != 64*1024 {
		t.Fatalf("preview byte limit = %d", tools.MaxToolOutputPreviewBytes)
	}
}
