package runner_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type contextSink struct {
	runner.NopSink
	iterations []runner.IterationCompleted
}

func (s *contextSink) OnIterationCompleted(event runner.IterationCompleted) {
	s.iterations = append(s.iterations, event)
}

func TestContextBreakdownPublishedForToolHistory(t *testing.T) {
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{{ToolCalls: []llm.ToolCall{
			{ID: "c1", Function: llm.ToolCallFunction{Name: "skill_load", Arguments: `{}`}},
			{ID: "c2", Function: llm.ToolCallFunction{Name: "agent_await", Arguments: `{}`}},
			{ID: "c3", Function: llm.ToolCallFunction{Name: "bash", Arguments: `{}`}},
			{ID: "c4", Function: llm.ToolCallFunction{Name: "instruction_load", Arguments: `{}`}},
		}}},
		{runnertest.ChunkText("ok")},
	})
	registry := tools.NewRegistry(
		contextTool{name: "skill_load", result: "skill body content"},
		contextTool{name: "agent_await", result: "agent summary"},
		contextTool{name: "bash", result: "$ ls"},
		contextTool{name: "instruction_load", result: "nested guidance"},
	)
	sink := &contextSink{}
	r := runner.New(client,
		runner.WithTools(registry),
		runner.WithPromptText("sys-prompt"),
		runner.WithSink(sink),
		runner.WithContextBreakdown(),
	)

	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "do the thing please"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}
	if len(sink.iterations) != 2 {
		t.Fatalf("iteration events = %d, want 2", len(sink.iterations))
	}
	breakdown := sink.iterations[0].Context
	if breakdown == nil {
		t.Fatal("first iteration context breakdown is nil")
	}
	if breakdown.SystemMsgs != 1 || breakdown.UserMsgs != 1 || breakdown.AssistantMsgs != 1 || breakdown.ToolMsgs != 4 {
		t.Errorf("message counts = sys%d user%d assistant%d tool%d", breakdown.SystemMsgs, breakdown.UserMsgs, breakdown.AssistantMsgs, breakdown.ToolMsgs)
	}
	if breakdown.SystemBytes != len("sys-prompt") || breakdown.UserBytes != len("do the thing please") {
		t.Errorf("system/user bytes = %d/%d", breakdown.SystemBytes, breakdown.UserBytes)
	}
	if breakdown.SkillBytes == 0 || breakdown.AgentBytes == 0 || breakdown.InstructionBytes == 0 {
		t.Errorf("classified tool bytes = skill%d agent%d instruction%d", breakdown.SkillBytes, breakdown.AgentBytes, breakdown.InstructionBytes)
	}
	other := breakdown.ToolBytes - breakdown.SkillBytes - breakdown.AgentBytes - breakdown.InstructionBytes
	if other == 0 {
		t.Errorf("other tool bytes = 0, total breakdown = %+v", *breakdown)
	}
}

type contextTool struct {
	name   tools.ToolName
	result string
}

func (tool contextTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: tool.name, Description: "test tool"}
}

func (tool contextTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: tool.result}, nil
}
