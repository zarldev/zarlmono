package google

import (
	"testing"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestApplyResponseFormat(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"v": map[string]any{"type": "string"}}}
	tests := []struct {
		name       string
		rf         llm.ResponseFormat
		wantMIME   string
		wantSchema bool
	}{
		{"text is a no-op", llm.ResponseFormat{}, "", false},
		{"json object sets mime only", llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}, "application/json", false},
		{"json schema sets mime + schema", llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)}, "application/json", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &genai.GenerateContentConfig{}
			applyResponseFormat(cfg, tc.rf)
			if cfg.ResponseMIMEType != tc.wantMIME {
				t.Errorf("ResponseMIMEType = %q, want %q", cfg.ResponseMIMEType, tc.wantMIME)
			}
			if got := cfg.ResponseJsonSchema != nil; got != tc.wantSchema {
				t.Errorf("ResponseJsonSchema present = %v, want %v", got, tc.wantSchema)
			}
		})
	}
}

func TestBuildConfigTemperatureGating(t *testing.T) {
	p := &Provider{}

	// Unset temperature must NOT pin Gemini to 0.0 — leave it for the server.
	if cfg := p.buildConfig(llm.CompletionRequest{}); cfg.Temperature != nil {
		t.Errorf("unset temperature should stay nil (server default), got %v", *cfg.Temperature)
	}
	// An explicit temperature is forwarded.
	if cfg := p.buildConfig(llm.CompletionRequest{Temperature: 0.7}); cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("temperature 0.7 should be forwarded, got %v", cfg.Temperature)
	}
}
