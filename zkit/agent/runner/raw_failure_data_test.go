package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type partialFailureTool struct{ data any }

func (partialFailureTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "partial", Description: "returns partial data and an error"}
}
func (tool partialFailureTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	result := tools.Failure(call.ID, tools.Validation("partial", "bad argument"))
	result.Data = tool.data
	return result, nil
}

func TestFailedToolRawDataExcludesErrorPresentation(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		want string
	}{
		{"text", "partial bytes\n", "partial bytes\n"},
		{"structured", struct {
			Text string `json:"text"`
		}{Text: "partial"}, `{"text":"partial"}`},
		{"absent", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputs := &recordingToolOutputSink{}
			history := &canonicalSink{}
			events := &rawHistorySink{}
			client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("call", "partial", `{}`)}, {runnertest.ChunkText("done")}})
			result := runner.New(client, runner.WithTools(tools.NewRegistry(partialFailureTool{data: tc.data})), runner.WithToolOutputSink(outputs), runner.WithHistorySink(history), runner.WithSink(events)).Run(t.Context(), runner.TaskSpec{Prompt: "test", MaxIterations: 2})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if len(outputs.records) != 1 || len(events.failures) != 1 {
				t.Fatal("missing failed result")
			}
			output := outputs.records[0]
			if output.Output != tc.want || output.Error == "" || output.Kind != tools.Kinds.VALIDATION || output.Success {
				t.Fatalf("raw output/classification: %+v", output)
			}
			if events.failures[0].RawOutput != tc.want {
				t.Fatalf("event raw output = %q", events.failures[0].RawOutput)
			}
			for _, message := range result.Messages {
				if message.Role == llm.RoleTool && (strings.Count(message.Content, "bad argument") != 1 || strings.Count(message.Content, "check the tool schema") != 1) {
					t.Fatalf("model error presentation = %q", message.Content)
				}
			}
			for _, record := range history.records {
				if record.Tool != nil && (record.Tool.Output != tc.want || strings.Count(record.Message.Content, "bad argument") != 1) {
					t.Fatalf("canonical failure = %+v", record)
				}
			}
		})
	}
}
