package anthropic

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestApplyResponseFormat(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"verdict": map[string]any{"type": "string"}},
	}

	// JSON-schema format → native OutputConfig.Format carries the schema.
	var p anthropic.MessageNewParams
	applyResponseFormat(&p, llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)})
	if p.OutputConfig.Format.Schema == nil {
		t.Fatal("JSON-schema format should set OutputConfig.Format.Schema")
	}
	if _, ok := p.OutputConfig.Format.Schema["properties"]; !ok {
		t.Errorf("schema should be forwarded verbatim, got %#v", p.OutputConfig.Format.Schema)
	}

	// JSON-object (no schema) → no-op: Anthropic has no schemaless JSON mode.
	var p2 anthropic.MessageNewParams
	applyResponseFormat(&p2, llm.ResponseFormat{Type: llm.ResponseFormatJSONObject})
	if p2.OutputConfig.Format.Schema != nil {
		t.Errorf("JSON-object mode should be a no-op on Anthropic, got %#v", p2.OutputConfig.Format.Schema)
	}

	// Text (zero value) → no-op.
	var p3 anthropic.MessageNewParams
	applyResponseFormat(&p3, llm.ResponseFormat{})
	if p3.OutputConfig.Format.Schema != nil {
		t.Error("text format should be a no-op")
	}
}

func TestApplyThinking(t *testing.T) {
	// Disabled → no-op, MaxTokens untouched.
	p := anthropic.MessageNewParams{MaxTokens: 4096}
	applyThinking(&p, llm.ThinkingConfig{})
	if p.Thinking.OfEnabled != nil {
		t.Error("disabled thinking should not set the param")
	}
	if p.MaxTokens != 4096 {
		t.Errorf("disabled thinking should not touch MaxTokens, got %d", p.MaxTokens)
	}

	// Enabled, sub-floor budget → clamps to 1024; MaxTokens grown for headroom.
	p2 := anthropic.MessageNewParams{MaxTokens: 500}
	applyThinking(&p2, llm.ThinkingConfig{Enabled: true})
	if p2.Thinking.OfEnabled == nil || p2.Thinking.OfEnabled.BudgetTokens != 1024 {
		t.Errorf("budget should clamp to 1024, got %#v", p2.Thinking.OfEnabled)
	}
	if p2.MaxTokens <= 1024 {
		t.Errorf("MaxTokens must exceed the thinking budget, got %d", p2.MaxTokens)
	}

	// Enabled, explicit budget, ample MaxTokens → MaxTokens untouched.
	p3 := anthropic.MessageNewParams{MaxTokens: 20000}
	applyThinking(&p3, llm.ThinkingConfig{Enabled: true, BudgetTokens: 8000})
	if p3.Thinking.OfEnabled == nil || p3.Thinking.OfEnabled.BudgetTokens != 8000 {
		t.Errorf("budget should be 8000, got %#v", p3.Thinking.OfEnabled)
	}
	if p3.MaxTokens != 20000 {
		t.Errorf("ample MaxTokens should be untouched, got %d", p3.MaxTokens)
	}
}
