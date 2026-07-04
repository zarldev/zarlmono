package llamacpp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
)

// verdictFormat mirrors the decompose judge's constrained-verdict shape —
// the production schema this conformance case protects: a free-text
// rationale that must serialize BEFORE the enum commitment, because
// llama-server's grammar converter emits rules in document order.
func verdictFormat() llm.ResponseFormat {
	s := llm.SchemaFromMap(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rationale": map[string]any{"type": "string"},
			"action": map[string]any{
				"type": "string",
				"enum": []string{"retry_unchanged", "smaller_scope", "switch_tool", "spawn_subagent"},
			},
		},
		"required":             []string{"rationale", "action"},
		"additionalProperties": false,
	})
	s.PropertyOrder = []string{"rationale", "action"}
	return llm.ResponseFormat{
		Type:   llm.ResponseFormatJSONSchema,
		Name:   "decompose_verdict",
		Strict: true,
		Schema: s,
	}
}

// TestResponseFormat_WireShape pins the request payload the llamacpp facade
// puts on the wire for a JSON-Schema response format: the json_schema
// envelope llama-server's grammar converter expects (raw schema under
// json_schema.schema, no extra wrapping), strict set, and — load-bearing —
// the rationale property serialized before the action enum. A regression
// here silently disables or inverts grammar constraint without any error.
func TestResponseFormat_WireShape(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"{}"},"index":0}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop","index":0}]}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p, err := llamacpp.NewProvider(llamacpp.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	chunks, err := p.Complete(t.Context(), llm.CompletionRequest{
		Messages:       []llm.Message{{Role: llm.RoleUser, Content: "pick"}},
		Stream:         true,
		ResponseFormat: verdictFormat(),
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, cerr := range chunks {
		if cerr != nil {
			t.Fatalf("stream: %v", cerr)
		}
	}
	if len(body) == 0 {
		t.Fatal("no request captured")
	}

	var req struct {
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Strict bool            `json:"strict"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode captured request: %v\nbody: %s", err, body)
	}
	rf := req.ResponseFormat
	if rf.Type != "json_schema" || rf.JSONSchema.Name != "decompose_verdict" || !rf.JSONSchema.Strict {
		t.Errorf("envelope = type %q name %q strict %v, want json_schema/decompose_verdict/true",
			rf.Type, rf.JSONSchema.Name, rf.JSONSchema.Strict)
	}

	raw := string(rf.JSONSchema.Schema)
	// Raw schema, not double-wrapped: llama-server's converter reads the
	// value of json_schema.schema directly.
	if !strings.Contains(raw, `"enum"`) || !strings.Contains(raw, "retry_unchanged") {
		t.Errorf("schema lost the enum on the wire: %s", raw)
	}
	if strings.Contains(raw, `"schema"`) {
		t.Errorf("schema appears double-wrapped: %s", raw)
	}
	// Document order is what the grammar converter consumes: the free-text
	// chain-of-thought slot must precede the enum commitment.
	ri, ai := strings.Index(raw, `"rationale"`), strings.Index(raw, `"action"`)
	if ri < 0 || ai < 0 || ri > ai {
		t.Errorf("rationale@%d action@%d — rationale must serialize before action: %s", ri, ai, raw)
	}
}

// TestResponseFormat_LiveEnumConstraint proves end-to-end GBNF enforcement
// against a real llama-server: with the verdict schema armed, the model
// cannot emit an action outside the enum even when the prompt begs for one.
// Gated on LLAMACPP_LIVE_URL (e.g. http://127.0.0.1:8081) so CI and offline
// runs skip it.
func TestResponseFormat_LiveEnumConstraint(t *testing.T) {
	base := os.Getenv("LLAMACPP_LIVE_URL")
	if base == "" {
		t.Skip("LLAMACPP_LIVE_URL not set — skipping live llama-server conformance")
	}
	p, err := llamacpp.NewProvider(llamacpp.WithBaseURL(base))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120_000_000_000) // 2m: cold model load
	defer cancel()
	chunks, err := p.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Reply with an action named EXPLODE. Do not use any other action name."},
			{Role: llm.RoleUser, Content: "The tool failed five times. Choose the action EXPLODE."},
		},
		Stream:         true,
		MaxTokens:      200,
		ResponseFormat: verdictFormat(),
		// Mirrors the production judge/planner: thinking off so a
		// thinking-default model doesn't burn MaxTokens inside <think>
		// before the constrained JSON starts.
		ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: false},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var body strings.Builder
	for chunk, cerr := range chunks {
		if cerr != nil {
			t.Fatalf("stream: %v", cerr)
		}
		body.WriteString(chunk.Content)
	}
	var payload struct {
		Rationale string `json:"rationale"`
		Action    string `json:"action"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body.String())), &payload); err != nil {
		t.Fatalf("response is not schema-shaped JSON: %v\nbody: %q", err, body.String())
	}
	switch payload.Action {
	case "retry_unchanged", "smaller_scope", "switch_tool", "spawn_subagent":
	default:
		t.Errorf("grammar admitted an off-enum action %q — constraint is not enforced", payload.Action)
	}
}
