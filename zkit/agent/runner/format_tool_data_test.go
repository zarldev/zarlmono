package runner_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type stringerData struct{}

func (stringerData) String() string { return "stringer-text" }

type dataTool struct {
	name tools.ToolName
	data any
}

func (tool dataTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: tool.name, Description: "returns structured data"}
}

func (tool dataTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: tool.data}, nil
}

type toolDataSink struct {
	runner.NopSink
	formatted map[string]string
}

func (s *toolDataSink) OnToolCompleted(event runner.ToolCompleted) {
	s.formatted[event.ToolName] = event.FormattedResult
}

func TestToolCompletedFormatsStructuredData(t *testing.T) {
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{{ToolCalls: []llm.ToolCall{
			{ID: "nil", Function: llm.ToolCallFunction{Name: "nil_data", Arguments: `{}`}},
			{ID: "string", Function: llm.ToolCallFunction{Name: "string_data", Arguments: `{}`}},
			{ID: "stringer", Function: llm.ToolCallFunction{Name: "stringer_data", Arguments: `{}`}},
			{ID: "map", Function: llm.ToolCallFunction{Name: "map_data", Arguments: `{}`}},
		}}},
		{runnertest.ChunkText("done")},
	})
	registry := tools.NewRegistry(
		dataTool{name: "nil_data", data: nil},
		dataTool{name: "string_data", data: "hi"},
		dataTool{name: "stringer_data", data: stringerData{}},
		dataTool{name: "map_data", data: map[string]string{"foo": "bar"}},
	)
	sink := &toolDataSink{formatted: make(map[string]string)}
	r := runner.New(client, runner.WithTools(registry), runner.WithSink(sink))

	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "format data"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}

	for name, want := range map[string]string{
		"nil_data":      "",
		"string_data":   "hi",
		"stringer_data": "stringer-text",
	} {
		if got := sink.formatted[name]; got != want {
			t.Errorf("%s formatted result = %q, want %q", name, got, want)
		}
	}
	got := sink.formatted["map_data"]
	if strings.Contains(got, "map[") || !strings.Contains(got, `"foo":"bar"`) {
		t.Errorf("map formatted result = %q, want JSON", got)
	}
	if len(sink.formatted) != 4 {
		t.Fatalf("formatted events = %d, want 4: %s", len(sink.formatted), fmt.Sprint(sink.formatted))
	}
}
