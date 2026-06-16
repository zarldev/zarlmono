package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// A full agentic exchange must survive the round-trip into SDK shape:
// system is hoisted out, the assistant's tool call becomes a tool_use
// block, and its result becomes a tool_result block on a following user
// message keyed by the same id.
func TestConvertMessagesToSDK_ToolCallRoundTrip(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "you are a tool"},
		{Role: llm.RoleUser, Content: "read x"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID:       "t1",
			Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"x"}`},
		}}},
		{Role: llm.RoleTool, ToolCallID: "t1", Content: "file contents"},
		{Role: llm.RoleAssistant, Content: "done"},
	}

	out := convertMessagesToSDK(msgs)

	if len(out) != 4 {
		t.Fatalf("want 4 sdk messages (system hoisted out), got %d", len(out))
	}

	// [0] user text — system was skipped, not emitted.
	if got := string(out[0].Role); got != "user" {
		t.Fatalf("msg[0] role = %q, want user", got)
	}
	if out[0].Content[0].OfText == nil || out[0].Content[0].OfText.Text != "read x" {
		t.Fatalf("msg[0] should be the user text block, got %+v", out[0].Content[0])
	}

	// [1] assistant tool_use carrying the verbatim arguments json.
	if got := string(out[1].Role); got != "assistant" {
		t.Fatalf("msg[1] role = %q, want assistant", got)
	}
	tu := out[1].Content[0].OfToolUse
	if tu == nil {
		t.Fatalf("msg[1] should carry a tool_use block, got %+v", out[1].Content[0])
	}
	if tu.ID != "t1" || tu.Name != "read" {
		t.Errorf("tool_use id/name = %q/%q, want t1/read", tu.ID, tu.Name)
	}
	if raw, ok := tu.Input.(json.RawMessage); !ok || string(raw) != `{"path":"x"}` {
		t.Errorf("tool_use input = %#v, want verbatim json.RawMessage", tu.Input)
	}

	// [2] tool result on a user message, keyed by the tool_use id.
	if got := string(out[2].Role); got != "user" {
		t.Fatalf("msg[2] role = %q, want user", got)
	}
	tr := out[2].Content[0].OfToolResult
	if tr == nil || tr.ToolUseID != "t1" {
		t.Fatalf("msg[2] should be a tool_result for t1, got %+v", out[2].Content[0])
	}

	// [3] the trailing assistant text.
	if out[3].Content[0].OfText == nil || out[3].Content[0].OfText.Text != "done" {
		t.Fatalf("msg[3] should be the assistant text 'done', got %+v", out[3].Content[0])
	}
}

// Results from a multi-call batch (the runner emits one role="tool"
// message each) must coalesce into a single user message so user and
// assistant turns alternate as the API requires.
func TestConvertMessagesToSDK_CoalescesToolResults(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "a", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"p":1}`}},
			{ID: "b", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"p":2}`}},
		}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "one"},
		{Role: llm.RoleTool, ToolCallID: "b", Content: "two"},
	}

	out := convertMessagesToSDK(msgs)

	if len(out) != 2 {
		t.Fatalf("want assistant + one coalesced user message, got %d", len(out))
	}
	if len(out[0].Content) != 2 {
		t.Errorf("assistant turn should carry 2 tool_use blocks, got %d", len(out[0].Content))
	}
	if got := string(out[1].Role); got != "user" {
		t.Fatalf("msg[1] role = %q, want user", got)
	}
	if len(out[1].Content) != 2 {
		t.Fatalf("the two tool results should coalesce into one user message, got %d blocks", len(out[1].Content))
	}
	if out[1].Content[0].OfToolResult == nil || out[1].Content[1].OfToolResult == nil {
		t.Errorf("both coalesced blocks should be tool_result, got %+v", out[1].Content)
	}
}

// An assistant turn with neither visible text nor a tool call is dropped
// (Anthropic rejects an empty assistant message), and empty arguments
// default to an object rather than an empty/invalid input.
func TestConvertMessagesToSDK_EmptyAssistantSkippedAndEmptyArgsDefault(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant}, // no content, no tool calls — skipped
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID:       "t1",
			Function: llm.ToolCallFunction{Name: "ping", Arguments: "  "},
		}}},
	}

	out := convertMessagesToSDK(msgs)

	if len(out) != 1 {
		t.Fatalf("empty assistant message should be skipped, got %d messages", len(out))
	}
	tu := out[0].Content[0].OfToolUse
	if tu == nil {
		t.Fatalf("want a tool_use block, got %+v", out[0].Content[0])
	}
	if raw, ok := tu.Input.(json.RawMessage); !ok || string(raw) != "{}" {
		t.Errorf("blank arguments should default to {} object, got %#v", tu.Input)
	}
}
