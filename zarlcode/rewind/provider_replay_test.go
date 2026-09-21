package rewind_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProviderReplayShape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider string
		message        llm.Message
	}{
		{"openai mixed content", "openai", llm.Message{Role: llm.RoleUser, Content: "lost", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "kept"}}}},
		{"codex mixed content", "openai-codex", llm.Message{Role: llm.RoleUser, Content: "lost", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "kept"}}}},
		{"openai reasoning", "openai", llm.Message{Role: llm.RoleAssistant, Content: "answer", ReasoningContent: "lost"}},
		{"anthropic empty user", "anthropic", llm.Message{Role: llm.RoleUser}},
		{"anthropic MIME contradiction", "anthropic", llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/jpeg"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := rewind.ValidateContext([]llm.Message{tc.message}, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateContext: %v", err)
			}
		})
	}
}

func TestNativeOutputOrdering(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*llm.Message)
		err    error
	}{
		{"consistent", func(*llm.Message) {}, nil},
		{"contradictory projection", func(m *llm.Message) { m.Content = "lost" }, rewind.ErrInvalid},
		{"wrong projection index", func(m *llm.Message) { m.ContentOutputIndex = llm.OutputPosition(2) }, rewind.ErrInvalid},
		{"duplicate native index", func(m *llm.Message) { m.ContinuationItems = append(m.ContinuationItems, m.ContinuationItems[0]) }, rewind.ErrInvalid},
		{"missing native index", func(m *llm.Message) { m.ContinuationItems[0].OutputIndex = nil }, rewind.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := llm.Message{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(0), ContinuationItems: []llm.ContinuationItem{{Provider: "anthropic", Format: "content_block.v1", Kind: "text", Data: []byte(`{"type":"text","text":"answer"}`), OutputIndex: llm.OutputPosition(0)}}}
			tc.mutate(&m)
			if err := rewind.ValidateContext([]llm.Message{m}, "anthropic"); !errors.Is(err, tc.err) {
				t.Fatalf("ValidateContext: %v, want %v", err, tc.err)
			}
		})
	}
}

func TestToolReplayMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider, output  string
		contentIndex, callIndex *int
	}{
		{"duplicate projected index", "openai-codex", "result", llm.OutputPosition(0), llm.OutputPosition(0)},
		{"ambiguous projected index", "anthropic", "result", llm.OutputPosition(0), nil},
		{"empty codex result", "openai-codex", "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			call := toolCall("call")
			call.OutputIndex = tc.callIndex
			messages := []llm.Message{{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: tc.contentIndex, ToolCalls: []llm.ToolCall{call}}, {Role: llm.RoleTool, ToolCallID: "call", Content: tc.output}}
			if err := rewind.ValidateContext(messages, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateContext: %v", err)
			}
		})
	}
}
