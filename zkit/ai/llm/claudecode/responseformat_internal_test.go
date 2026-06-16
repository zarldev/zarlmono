package claudecode

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestUsageFromLine(t *testing.T) {
	// Non-usage lines yield nil.
	if u := usageFromLine(`{"type":"stream_event","event":{"type":"content_block_stop"}}`); u != nil {
		t.Errorf("a line without usage should yield nil, got %#v", u)
	}

	// Terminal result event: usage at the top level. PromptTokens sums
	// base + cache-creation + cache-read; CachedTokens is the read subset.
	result := `{"type":"result","subtype":"success","usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":50}}`
	u := usageFromLine(result)
	if u == nil {
		t.Fatal("result event should yield usage")
	}
	if u.PromptTokens != 150 || u.CompletionTokens != 50 || u.TotalTokens != 200 || u.CachedTokens != 30 {
		t.Errorf("unexpected usage: %#v", u)
	}

	// Assistant event: usage nested under message.
	asst := `{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":5}}}`
	if u := usageFromLine(asst); u == nil || u.PromptTokens != 10 || u.CompletionTokens != 5 {
		t.Errorf("assistant-event usage should be read from message.usage, got %#v", u)
	}
}

func TestBuildPrompt_ResponseFormatDirective(t *testing.T) {
	base := llm.CompletionRequest{Messages: []llm.Message{{Role: "user", Content: "hi"}}}

	// Text default: no directive appended.
	if got := buildPrompt(base); strings.Contains(got, "valid JSON object") {
		t.Errorf("text default should add no JSON directive, got:\n%s", got)
	}

	// JSON object: directive present, no schema.
	base.ResponseFormat = llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}
	if got := buildPrompt(base); !strings.Contains(got, "single valid JSON object") {
		t.Errorf("json-object should add a directive, got:\n%s", got)
	}

	// JSON schema: directive embeds the schema verbatim.
	base.ResponseFormat = llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Schema: llm.SchemaFromMap(map[string]any{"type": "object", "properties": map[string]any{"verdict": map[string]any{"type": "string"}}}),
	}
	got := buildPrompt(base)
	if !strings.Contains(got, "conforms to this JSON Schema") {
		t.Errorf("json-schema should reference the schema, got:\n%s", got)
	}
	if !strings.Contains(got, `"verdict"`) {
		t.Errorf("json-schema directive should embed the schema verbatim, got:\n%s", got)
	}
}
