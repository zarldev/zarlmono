package rewind_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestCheckpointRejectsLossyNativeReplay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, provider, format, kind, data string }{
		{"duplicate", "openai-codex", "responses.reasoning.v1", "reasoning", `{"type":"reasoning","id":"one","id":"two","encrypted_content":"opaque"}`},
		{"unknown thinking field", "anthropic", "content_block.v1", "thinking", `{"type":"thinking","thinking":"text","signature":"signed","future":"private-canary"}`},
		{"nested citation field", "anthropic", "content_block.v1", "text", `{"type":"text","text":"text","citations":[{"type":"char_location","future":"private-canary"}]}`},
		{"text wrong shape", "anthropic", "content_block.v1", "text", `{"type":"text","text":{"future":"private-canary"}}`},
		{"text null", "anthropic", "content_block.v1", "text", `{"type":"text","text":null}`},
		{"unencrypted responses reasoning", "openai", "responses.output_item", "reasoning", `{"type":"reasoning","encrypted_content":""}`},
		{"missing thinking", "anthropic", "content_block.v1", "thinking", `{"type":"thinking","signature":"signed"}`},
		{"empty citations", "anthropic", "content_block.v1", "text", `{"type":"text","text":"text","citations":[]}`},
		{"missing text", "anthropic", "content_block.v1", "text", `{"type":"text"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			item := llm.ContinuationItem{Provider: tc.provider, Format: tc.format, Kind: tc.kind, Data: []byte(tc.data), OutputIndex: llm.OutputPosition(0)}
			if err := rewind.ValidateContext([]llm.Message{{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{item}}}, tc.provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("ValidateContext: %v", err)
			}
		})
	}
}

func TestCheckpointRejectsLossyMessageReplay(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"openai", "openai-codex", "anthropic"} {
		for _, message := range []llm.Message{
			{Role: llm.RoleAssistant},
			{Role: llm.RoleSystem, Content: "omitted"},
			{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeText}}},
			{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "https://example.test/image", DataURI: "data:image/png;base64,YQ=="}}}},
		} {
			if err := rewind.ValidateContext([]llm.Message{message}, provider); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("%s: %v", provider, err)
			}
		}
		image := &llm.ImageData{URL: "https://example.test/image", MIMEType: "image/png"}
		if provider == "anthropic" {
			image.MIMEType = ""
			image.Detail = "high"
		}
		if err := rewind.ValidateContext([]llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: image}}}}, provider); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("%s unsupported image fields: %v", provider, err)
		}
	}
}

func TestCheckpointRequiresCanonicalPlanStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"done", "in-progress", "", "private-canary"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			record := captureRecord(t, captureInput(t))
			mutatePayload(t, &record, func(p map[string]any) {
				p["plan"].(map[string]any)["steps"].([]any)[0].(map[string]any)["status"] = status
			})
			if _, err := rewind.Decode(record); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("Decode: %v", err)
			}
		})
	}
	input := captureInput(t)
	input.Plan.Steps[0].Status, _ = code.ParseStepStatus("private-canary")
	if _, err := rewind.Capture(input); !errors.Is(err, rewind.ErrInvalid) {
		t.Fatalf("invalid status: %v", err)
	}
}
