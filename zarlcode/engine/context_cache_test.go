package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/google/go-cmp/cmp"
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestContextCache_ThreadsContext(t *testing.T) {
	var c engine.ContextCache

	var ctx1 []llm.Message
	c.Run("first", func(spec runner.TaskSpec) runner.TaskResult {
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
	c.Run("second", func(spec runner.TaskSpec) runner.TaskResult {
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
	if len(c.Snapshot()) != 4 {
		t.Errorf("context after turn 2 should be 4 messages, got %d", len(c.Snapshot()))
	}
}

func TestContextCache_FailedTurnWithoutMessagesKeepsContext(t *testing.T) {
	var c engine.ContextCache
	c.Restore([]llm.Message{{Role: "user", Content: "x"}})
	c.Run("p", func(runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Reason: runner.TerminalError, Err: errors.New("boom")}
	})
	if len(c.Snapshot()) != 1 {
		t.Errorf("a failed turn with no partial messages must not clobber context, got %d", len(c.Snapshot()))
	}
}

// TestContextCache_FailedTurnRecordsPartialWork asserts that a terminal
// error which still carries the context accumulated up to the failure
// (e.g. a provider stream flake after several productive tool
// iterations) preserves that work for the next turn rather than dropping
// it. The runner guarantees the partial context is coherent.
func TestContextCache_FailedTurnRecordsPartialWork(t *testing.T) {
	var c engine.ContextCache
	c.Restore([]llm.Message{{Role: "user", Content: "start"}})
	partial := []llm.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: "tool output"},
	}
	c.Run("p", func(runner.TaskSpec) runner.TaskResult {
		return runner.TaskResult{Messages: partial, Reason: runner.TerminalError, Err: errors.New("provider stream cut")}
	})
	if len(c.Snapshot()) != 3 {
		t.Fatalf("failed turn should record %d partial messages, got %d", len(partial), len(c.Snapshot()))
	}
	if c.Snapshot()[2].Content != "tool output" {
		t.Errorf("partial tool result not threaded: %q", c.Snapshot()[2].Content)
	}
}

func TestContextCache_CompactNowFailureLeavesContextUnchanged(t *testing.T) {
	t.Parallel()
	cached := []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "second"},
		{Role: llm.RoleUser, Content: "third"},
	}
	var c engine.ContextCache
	c.Restore(cached)
	_, err := c.Compact(t.Context(), agentcompact.Func(func(context.Context, []llm.Message, int) (agentcompact.Result, error) {
		return agentcompact.Result{}, errors.New("compact failed")
	}), nil)
	if err == nil {
		t.Fatal("expected compaction error")
	}
	if diff := cmp.Diff(cached, c.Snapshot()); diff != "" {
		t.Fatalf("context changed after failed compaction (-want +got):\n%s", diff)
	}
}

func TestContextCache_OwnsNestedMessageState(t *testing.T) {
	t.Parallel()
	input := []llm.Message{{
		Role:      llm.RoleUser,
		Parts:     []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "original"}}},
		ToolCalls: []llm.ToolCall{{ID: "original-call"}},
	}}
	var cache engine.ContextCache
	cache.Restore(input)
	input[0].Parts[0].Image.URL = "mutated-input"
	input[0].ToolCalls[0].ID = "mutated-input"

	snapshot := cache.Snapshot()
	snapshot[0].Parts[0].Image.URL = "mutated-snapshot"
	snapshot[0].ToolCalls[0].ID = "mutated-snapshot"

	got := cache.Snapshot()[0]
	if got.Parts[0].Image.URL != "original" || got.ToolCalls[0].ID != "original-call" {
		t.Fatalf("owned context was mutated: %#v", got)
	}
}
func TestContextCache_CompactNowOwnsCompactorHandoffs(t *testing.T) {
	t.Parallel()

	cached := []llm.Message{{
		Role:  llm.RoleUser,
		Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "input"}}},
	}}
	result := []llm.Message{{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "result"}},
	}}
	var compactorInput []llm.Message
	var cache engine.ContextCache
	cache.Restore(cached)
	if _, err := cache.Compact(t.Context(), agentcompact.Func(func(_ context.Context, history []llm.Message, _ int) (agentcompact.Result, error) {
		compactorInput = history
		return agentcompact.Result{History: result}, nil
	}), nil); err != nil {
		t.Fatal(err)
	}
	compactorInput[0].Parts[0].Image.URL = "mutated-input"
	result[0].ToolCalls[0].ID = "mutated-result"

	got := cache.Snapshot()
	if len(got) != 1 || got[0].ToolCalls[0].ID != "result" {
		t.Fatalf("compactor handoff mutated owned context: %#v", got)
	}
}

func TestContextCache_OwnsRunnerHandoffs(t *testing.T) {
	t.Parallel()
	var cache engine.ContextCache
	cache.Restore([]llm.Message{{
		Role:  llm.RoleUser,
		Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "context"}}},
	}})
	result := []llm.Message{{
		Role:      llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{ID: "result"}},
	}}
	cache.Run("next", func(spec runner.TaskSpec) runner.TaskResult {
		spec.Context[0].Parts[0].Image.URL = "mutated-exec"
		return runner.TaskResult{Messages: result}
	})
	result[0].ToolCalls[0].ID = "mutated-result"

	got := cache.Snapshot()
	if len(got) != 1 || got[0].ToolCalls[0].ID != "result" {
		t.Fatalf("runner handoff mutated owned context: %#v", got)
	}
}
