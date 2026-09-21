package rewind_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestReplayPreservesSparseOutputPositions(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, index := range []int{1, 2} {
				item := llm.ContinuationItem{Provider: provider, OutputIndex: llm.OutputPosition(index), Format: "content_block.v1", Kind: "text", Data: []byte(`{"type":"text","text":"answer"}`)}
				if provider == "openai-codex" {
					item.Format, item.Kind = "responses.reasoning.v1", "reasoning"
					item.Data = []byte(`{"type":"reasoning","id":"item","encrypted_content":"opaque"}`)
				}
				message := llm.Message{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{item}}
				if index == 2 {
					first := item
					first.OutputIndex = llm.OutputPosition(0)
					message.ContinuationItems = append([]llm.ContinuationItem{first}, item)
				}
				messages := []llm.Message{message}
				encoded, err := rewind.EncodeResume(1, messages, rewind.Target{Provider: provider, Model: "test-model"}, "turn", 1, rewind.InitialContinuation{})
				if err != nil {
					t.Fatalf("position %d: %v", index, err)
				}
				decoded, err := rewind.DecodeResume(encoded)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(decoded.Context, messages) {
					t.Fatal("sparse positions changed during save/resume")
				}
			}
		})
	}
}

func TestReplayRejectsUnboundReasoningProjection(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"anthropic", "openai-codex"} {
		message := llm.Message{Role: llm.RoleAssistant, Content: "answer", ReasoningContent: "would be omitted"}
		if err := rewind.ValidateContext([]llm.Message{message}, provider); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("%s: %v", provider, err)
		}
	}
}
