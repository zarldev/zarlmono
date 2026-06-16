package openaicodex

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestBuildRequest_ResponseFormat(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"v": map[string]any{"type": "string"}},
	}

	// JSON schema → text.format with flat name/schema/strict.
	rr := buildRequest(llm.CompletionRequest{
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Name: "verdict", Schema: llm.SchemaFromMap(schema), Strict: true},
	}, "gpt-5", "")
	if rr.Text == nil || rr.Text.Format == nil {
		t.Fatal("JSON-schema format should set text.format")
	}
	if f := rr.Text.Format; f.Type != "json_schema" || f.Name != "verdict" || !f.Strict || f.Schema == nil {
		t.Errorf("unexpected format: %#v", f)
	}

	// JSON object → text.format type only.
	rr2 := buildRequest(llm.CompletionRequest{
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
	}, "gpt-5", "")
	if rr2.Text == nil || rr2.Text.Format == nil || rr2.Text.Format.Type != "json_object" {
		t.Errorf("json-object format expected, got %#v", rr2.Text)
	}

	// Unconstrained text → no format directive.
	rr3 := buildRequest(llm.CompletionRequest{}, "gpt-5", "")
	if rr3.Text != nil && rr3.Text.Format != nil {
		t.Errorf("unconstrained text should not set format, got %#v", rr3.Text.Format)
	}

	// Empty schema name defaults to "response" (Responses API requires one).
	rr4 := buildRequest(llm.CompletionRequest{
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)},
	}, "gpt-5", "")
	if rr4.Text.Format.Name != "response" {
		t.Errorf("empty schema name should default to 'response', got %q", rr4.Text.Format.Name)
	}

	// Verbosity and format share the text block — neither clobbers the other.
	rr5 := buildRequest(llm.CompletionRequest{
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
		Options:        llm.ModelOptions{"text_verbosity": "low"},
	}, "gpt-5", "")
	if rr5.Text == nil || rr5.Text.Format == nil || rr5.Text.Verbosity != "low" {
		t.Errorf("verbosity + format should coexist, got %#v", rr5.Text)
	}
}
