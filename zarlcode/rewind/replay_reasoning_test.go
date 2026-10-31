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

func TestDeepSeekReasoningAndVisionLegacyReplay(t *testing.T) {
	t.Parallel()

	reasoning := []llm.Message{{Role: llm.RoleAssistant, Content: "answer", ReasoningContent: "private thought"}}
	if err := rewind.ValidateLegacyContext(reasoning, "deepseek"); err != nil {
		t.Fatalf("deepseek reasoning rejected on legacy resume: %v", err)
	}

	vision := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{
		Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ=="},
	}}}}
	if err := rewind.ValidateLegacyContext(vision, "deepseek"); err != nil {
		t.Fatalf("deepseek vision rejected on legacy resume: %v", err)
	}

	// Ordinary field/display replay does not qualify a route for a versioned
	// exact envelope. Never silently promote historical DeepSeek sessions.
	if err := rewind.ValidateContext(reasoning, "deepseek"); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("exact DeepSeek context = %v, want ErrInvalid", err)
	}
	if _, err := rewind.EncodeResume(1, reasoning, rewind.Target{Provider: "deepseek", Model: "deepseek-flash"}, "turn", 1, rewind.InitialContinuation{}); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("encode unqualified DeepSeek resume = %v, want ErrInvalid", err)
	}
}
