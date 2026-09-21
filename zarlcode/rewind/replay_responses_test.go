package rewind_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestResponsesResumeBindsSavedModelRoute(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.6", "gpt-6-astra", "gpt-4o", "unknown"} {
		t.Run(model, func(t *testing.T) {
			t.Parallel()
			for _, message := range []llm.Message{
				{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(0)},
				{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(1), ReasoningContent: "Considering", ContinuationItems: []llm.ContinuationItem{{Provider: "openai", Format: "responses.output_item", Kind: "reasoning", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"type":"reasoning","encrypted_content":"private-canary","summary":[]}`)}}},
			} {
				messages := []llm.Message{message}
				encoded, err := rewind.EncodeResume(1, messages, rewind.Target{Provider: "openai", Model: model}, "turn", 1, rewind.InitialContinuation{})
				if model == "gpt-4o" || model == "unknown" {
					if !errors.Is(err, rewind.ErrInvalid) {
						t.Fatalf("chat route admitted Responses metadata: %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := rewind.DecodeResume(encoded)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(decoded.Context, messages) {
					t.Fatal("round trip changed native or display reasoning")
				}
			}
		})
	}
}

func TestResponsesNativeProjectionValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*llm.Message)
		err    error
	}{
		{"matching", func(*llm.Message) {}, nil},
		{"contradictory text", func(m *llm.Message) { m.Content = "lost" }, rewind.ErrInvalid},
		{"contradictory position", func(m *llm.Message) { m.ContentOutputIndex = llm.OutputPosition(1) }, rewind.ErrInvalid},
		{"missing position", func(m *llm.Message) { m.ContinuationItems[0].OutputIndex = nil }, rewind.ErrInvalid},
		{"foreign route", func(m *llm.Message) { m.ContinuationItems[0].Provider = "anthropic" }, rewind.ErrInvalid},
		{"contradictory identity", func(m *llm.Message) { m.ContinuationItems[0].ID = "other" }, rewind.ErrInvalid},
		{"duplicate field", func(m *llm.Message) { m.ContinuationItems[0].Data = []byte(`{"type":"message","type":"reasoning"}`) }, rewind.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := llm.Message{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(0), ContinuationItems: []llm.ContinuationItem{{Provider: "openai", Format: "responses.output_item", Kind: "message", ID: "msg_1", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"answer"}]}`)}}}
			tc.mutate(&m)
			if err := rewind.ValidateContext([]llm.Message{m}, "openai"); !errors.Is(err, tc.err) {
				t.Fatalf("validation=%v, want %v", err, tc.err)
			}
		})
	}
}
