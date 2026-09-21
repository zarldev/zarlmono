package rewind_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestNativeReasoningReplayValidation(t *testing.T) {
	t.Parallel()
	for _, item := range []llm.ContinuationItem{
		{Provider: "anthropic", Format: "content_block.v1", Kind: "thinking", Data: []byte(`{"type":"thinking","thinking":"Considering","signature":"private-canary"}`)},
		{Provider: "openai", Format: "responses.output_item", Kind: "reasoning", Data: []byte(`{"type":"reasoning","encrypted_content":"private-canary","summary":[{"type":"summary_text","text":"Considering"}]}`)},
	} {
		t.Run(item.Provider, func(t *testing.T) {
			t.Parallel()
			for _, projection := range []string{"", "Considering", "contradictory-private-canary"} {
				message := llm.Message{Role: llm.RoleAssistant, ReasoningContent: projection, ContinuationItems: []llm.ContinuationItem{item}}
				err := rewind.ValidateContext([]llm.Message{message}, item.Provider)
				if item.Provider == "anthropic" && strings.HasPrefix(projection, "contradictory") {
					if !errors.Is(err, rewind.ErrInvalid) || strings.Contains(err.Error(), "private-canary") {
						t.Fatalf("contradictory reasoning not safely rejected: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
