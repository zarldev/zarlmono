package claudecode_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProviderResponseFormatDirective(t *testing.T) {
	cases := []struct {
		name     string
		format   llm.ResponseFormat
		contains []string
		absent   string
	}{
		{name: "text default", absent: "valid JSON object"},
		{name: "JSON object", format: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}, contains: []string{"single valid JSON object"}},
		{name: "JSON schema", format: llm.ResponseFormat{
			Type: llm.ResponseFormatJSONSchema,
			Schema: llm.SchemaFromMap(map[string]any{
				"type":       "object",
				"properties": map[string]any{"verdict": map[string]any{"type": "string"}},
			}),
		}, contains: []string{"conforms to this JSON Schema", `"verdict"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, prompt := complete(t, "", llm.CompletionRequest{
				Messages:       []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				ResponseFormat: tc.format,
			})
			for _, want := range tc.contains {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q in:\n%s", want, prompt)
				}
			}
			if tc.absent != "" && strings.Contains(prompt, tc.absent) {
				t.Errorf("prompt unexpectedly contains %q in:\n%s", tc.absent, prompt)
			}
		})
	}
}
