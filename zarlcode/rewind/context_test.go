package rewind_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func toolCall(id string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"file"}`}}
}

func TestValidateContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		messages []llm.Message
		err      error
	}{
		{name: "empty"},
		{name: "complete tool pair", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("call")}}, {Role: llm.RoleTool, ToolCallID: "call", Content: "result"}}},
		{name: "unknown role", messages: []llm.Message{{Role: "unknown"}}, err: rewind.ErrInvalid},
		{name: "user tool calls", messages: []llm.Message{{Role: llm.RoleUser, ToolCalls: []llm.ToolCall{toolCall("call")}}}, err: rewind.ErrInvalid},
		{name: "dangling call", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("call")}}}, err: rewind.ErrInvalid},
		{name: "orphan result", messages: []llm.Message{{Role: llm.RoleTool, ToolCallID: "call"}}, err: rewind.ErrInvalid},
		{name: "duplicate call", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("call"), toolCall("call")}}}, err: rewind.ErrInvalid},
		{name: "out of order results", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("a"), toolCall("b")}}, {Role: llm.RoleTool, ToolCallID: "b"}, {Role: llm.RoleTool, ToolCallID: "a"}}, err: rewind.ErrInvalid},
		{name: "interrupted pair", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("a")}}, {Role: llm.RoleUser, Content: "interjection"}, {Role: llm.RoleTool, ToolCallID: "a"}}, err: rewind.ErrInvalid},
		{name: "negative index", messages: []llm.Message{{Role: llm.RoleAssistant, ContentOutputIndex: llm.OutputPosition(-1)}}, err: rewind.ErrInvalid},
		{name: "wrong part shape", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Image: &llm.ImageData{URL: "https://example.test/image"}}}}}, err: rewind.ErrInvalid},
		{name: "missing image", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage}}}}, err: rewind.ErrInvalid},
		{name: "unknown part", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: "future"}}}}, err: rewind.ErrInvalid},
		{name: "empty audio", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeAudio, Audio: &llm.AudioData{}}}}}, err: rewind.ErrInvalid},
		{name: "historical image", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ=="}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := rewind.ValidateContext(tc.messages, "openai"); !errors.Is(err, tc.err) {
				t.Fatalf("ValidateContext=%v, want %v", err, tc.err)
			}
		})
	}
}

func TestValidateToolArgumentsAndIndexes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*llm.ToolCall)
	}{
		{"malformed arguments", func(c *llm.ToolCall) { c.Function.Arguments = "private-canary" }},
		{"negative index", func(c *llm.ToolCall) { c.OutputIndex = llm.OutputPosition(-1) }},
		{"missing ID", func(c *llm.ToolCall) { c.ID = "" }},
		{"wrong type", func(c *llm.ToolCall) { c.Type = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			call := toolCall("call")
			tc.mutate(&call)
			if err := rewind.ValidateContext([]llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, ToolCallID: "call"}}, "openai"); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateContext: %v", err)
			}
		})
	}
}

func TestNativeContinuationRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ provider, format, kind, data string }{
		{"anthropic", "content_block.v1", "thinking", `{"type":"thinking","thinking":"private-canary","signature":"signed"}`},
		{"anthropic", "content_block.v1", "redacted_thinking", `{"type":"redacted_thinking","data":"opaque"}`},
		{"anthropic", "content_block.v1", "text", `{"type":"text","text":"visible"}`},
		{"openai", "responses.output_item", "message", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"visible"}]}`},
		{"openai", "responses.output_item", "reasoning", `{"type":"reasoning","encrypted_content":"opaque"}`},
		{"openai-codex", "responses.reasoning.v1", "reasoning", `{"type":"reasoning","id":"item","encrypted_content":"opaque"}`},
	} {
		t.Run(tc.provider+"/"+tc.kind, func(t *testing.T) {
			t.Parallel()
			item := llm.ContinuationItem{Provider: tc.provider, Format: tc.format, Kind: tc.kind, Data: []byte(tc.data), OutputIndex: llm.OutputPosition(0)}
			messages := []llm.Message{{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{item}}}
			if err := rewind.ValidateContext(messages, tc.provider); err != nil {
				t.Fatal(err)
			}
			if err := rewind.ValidateContext(messages, "other"); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("foreign provider: %v", err)
			}
			messages[0].ContinuationItems[0].Format = "future-format"
			if err := rewind.ValidateContext(messages, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("unknown format: %v", err)
			}
			messages[0].ContinuationItems[0] = item
			messages[0].ContinuationItems[0].Data = []byte("private-canary")
			if err := rewind.ValidateContext(messages, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("malformed data: %v", err)
			}
		})
	}
}
