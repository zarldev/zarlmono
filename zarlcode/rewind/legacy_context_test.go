package rewind_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestLegacyContextDoesNotQualifyExactReplay(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"", "llamacpp", "google", "custom"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name     string
				messages []llm.Message
			}{
				{name: "empty draft"},
				{name: "text", messages: []llm.Message{{Role: llm.RoleUser, Content: "historical prompt"}, {Role: llm.RoleAssistant, Content: "historical answer"}}},
				{name: "tool pair", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("call")}}, {Role: llm.RoleTool, ToolCallID: "call", Content: "result"}}},
				{name: "host observation", messages: []llm.Message{{Role: llm.RoleUser, Content: "historical evidence", Observation: llm.ObservationProvenance{Version: 1, ID: "completion"}}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := rewind.ValidateLegacyContext(tc.messages, provider); err != nil {
						t.Fatalf("legacy context: %v", err)
					}
					if err := rewind.ValidateContext(tc.messages, provider); !errors.Is(err, rewind.ErrInvalid) {
						t.Fatalf("exact context = %v, want unsupported provider rejection", err)
					}
					if _, err := rewind.EncodeResume(1, tc.messages, rewind.Target{Provider: provider, Model: "model"}, "turn", 1, rewind.InitialContinuation{}); !errors.Is(err, rewind.ErrInvalid) {
						t.Fatalf("exact resume = %v, want unsupported provider rejection", err)
					}
				})
			}
		})
	}
}

func TestLegacyContextStillRejectsMalformedReplay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		messages []llm.Message
	}{
		{name: "unknown role", messages: []llm.Message{{Role: "unknown", Content: "private-canary"}}},
		{name: "dangling call", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("call")}}}},
		{name: "orphan result", messages: []llm.Message{{Role: llm.RoleTool, ToolCallID: "call", Content: "private-canary"}}},
		{name: "duplicate call", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("call"), toolCall("call")}}}},
		{name: "out of order", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall("a"), toolCall("b")}}, {Role: llm.RoleTool, ToolCallID: "b"}, {Role: llm.RoleTool, ToolCallID: "a"}}},
		{name: "malformed arguments", messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call", Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: "private-canary"}}}}, {Role: llm.RoleTool, ToolCallID: "call"}}},
		{name: "negative index", messages: []llm.Message{{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(-1)}}},
		{name: "unbound native continuation", messages: []llm.Message{{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "anthropic", Format: "content_block.v1", Kind: "thinking", Data: []byte(`{"type":"thinking","thinking":"private-canary","signature":"signed"}`)}}}}},
		{name: "unknown observation version", messages: []llm.Message{{Role: llm.RoleUser, Content: "evidence", Observation: llm.ObservationProvenance{Version: 2, ID: "completion"}}}},
		{name: "misplaced observation", messages: []llm.Message{{Role: llm.RoleAssistant, Content: "answer", Observation: llm.ObservationProvenance{Version: 1, ID: "completion"}}}},
		{name: "unsupported part", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: "future"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, provider := range []string{"", "llamacpp", "openai"} {
				if err := rewind.ValidateLegacyContext(tc.messages, provider); !errors.Is(err, rewind.ErrInvalid) {
					t.Fatalf("provider %q: legacy context = %v, want invalid", provider, err)
				}
			}
		})
	}
}
