package engine

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestConversation_ThreadsHistory(t *testing.T) {
	var c conversation

	var ctx1 []llm.Message
	c.run("first", func(spec runner.TaskSpec) runner.TaskResult {
		ctx1 = spec.Context
		if spec.Prompt != "first" {
			t.Errorf("turn 1 prompt = %q", spec.Prompt)
		}
		return runner.TaskResult{Messages: []llm.Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "ok"},
		}}
	})
	if len(ctx1) != 0 {
		t.Errorf("turn 1 context should be empty, got %d", len(ctx1))
	}

	var ctx2 []llm.Message
	c.run("second", func(spec runner.TaskSpec) runner.TaskResult {
		ctx2 = spec.Context
		return runner.TaskResult{Messages: append(append([]llm.Message{}, spec.Context...),
			llm.Message{Role: "user", Content: "second"},
			llm.Message{Role: "assistant", Content: "done"},
		)}
	})
	if len(ctx2) != 2 {
		t.Fatalf("turn 2 context should carry turn 1's 2 messages, got %d", len(ctx2))
	}
	if ctx2[1].Content != "ok" {
		t.Errorf("turn 2 context[1] = %q, want %q", ctx2[1].Content, "ok")
	}
	if len(c.history) != 4 {
		t.Errorf("history after turn 2 should be 4 messages, got %d", len(c.history))
	}
}

func TestConversation_FailedTurnWithoutMessagesKeepsHistory(t *testing.T) {
	c := conversation{history: []llm.Message{{Role: "user", Content: "x"}}}
	c.run("p", func(runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Reason: runner.TerminalError, Err: errors.New("boom")}
	})
	if len(c.history) != 1 {
		t.Errorf("a failed turn with no partial messages must not clobber history, got %d", len(c.history))
	}
}

// TestConversation_FailedTurnRecordsPartialWork asserts that a terminal
// error which still carries the history accumulated up to the failure
// (e.g. a provider stream flake after several productive tool
// iterations) preserves that work for the next turn rather than dropping
// it. The runner guarantees the partial history is coherent.
func TestConversation_FailedTurnRecordsPartialWork(t *testing.T) {
	c := conversation{history: []llm.Message{{Role: "user", Content: "start"}}}
	partial := []llm.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: "tool output"},
	}
	c.run("p", func(runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Messages: partial, Reason: runner.TerminalError, Err: errors.New("provider stream cut")}
	})
	if len(c.history) != 3 {
		t.Fatalf("failed turn should record %d partial messages, got %d", len(partial), len(c.history))
	}
	if c.history[2].Content != "tool output" {
		t.Errorf("partial tool result not threaded: %q", c.history[2].Content)
	}
}
