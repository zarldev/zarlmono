package deepseek

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// json_schema must be downgraded to json_object (DeepSeek rejects the
// json_schema variant outright), and the schema + the "json" keyword
// must land in the prompt so the json_object request is itself valid.
func TestAdaptResponseFormatDowngradesJSONSchema(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"agent": map[string]any{"type": "string"}},
	}
	in := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "route the task"},
			{Role: roleUser, Content: "Task: fix the bug"},
		},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: llm.SchemaFromMap(schema)},
	}

	got := adaptResponseFormat(in)

	if got.ResponseFormat.Type != llm.ResponseFormatJSONObject {
		t.Fatalf("type = %q, want json_object", got.ResponseFormat.Type)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Role != roleUser {
		t.Fatalf("directive landed on %q message, want user", last.Role)
	}
	if !strings.Contains(strings.ToLower(last.Content), "json") {
		t.Errorf("user message missing 'json' keyword: %q", last.Content)
	}
	if !strings.Contains(last.Content, `"agent"`) {
		t.Errorf("user message missing schema shape: %q", last.Content)
	}
	if !strings.HasPrefix(last.Content, "Task: fix the bug") {
		t.Errorf("directive replaced rather than appended to user content: %q", last.Content)
	}
}

// The input request (and its Messages slice) must not be mutated — the
// caller may reuse it.
func TestAdaptResponseFormatDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	in := llm.CompletionRequest{
		Messages:       []llm.Message{{Role: roleUser, Content: "Task: x"}},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema},
	}

	adaptResponseFormat(in)

	if in.Messages[0].Content != "Task: x" {
		t.Errorf("input message mutated: %q", in.Messages[0].Content)
	}
	if in.ResponseFormat.Type != llm.ResponseFormatJSONSchema {
		t.Errorf("input response format mutated: %q", in.ResponseFormat.Type)
	}
}

// Non-JSON response formats pass through untouched.
func TestAdaptResponseFormatLeavesTextUntouched(t *testing.T) {
	t.Parallel()

	in := llm.CompletionRequest{
		Messages:       []llm.Message{{Role: roleUser, Content: "hello"}},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatText},
	}

	got := adaptResponseFormat(in)

	if len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
		t.Errorf("messages changed: %+v", got.Messages)
	}
	if got.ResponseFormat.Type != llm.ResponseFormatText {
		t.Errorf("type changed to %q", got.ResponseFormat.Type)
	}
}

// json_object with a prompt that already says "json" needs no injected
// directive; one that doesn't gets the keyword added so DeepSeek won't
// 400 on the missing-keyword rule.
func TestAdaptResponseFormatJSONObjectKeyword(t *testing.T) {
	t.Parallel()

	t.Run("keyword present: untouched", func(t *testing.T) {
		t.Parallel()
		in := llm.CompletionRequest{
			Messages:       []llm.Message{{Role: roleUser, Content: "reply as JSON please"}},
			ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
		}
		got := adaptResponseFormat(in)
		if len(got.Messages) != 1 {
			t.Fatalf("messages = %d, want 1 (no directive needed)", len(got.Messages))
		}
	})

	t.Run("keyword absent: directive added", func(t *testing.T) {
		t.Parallel()
		in := llm.CompletionRequest{
			Messages:       []llm.Message{{Role: roleUser, Content: "reply with the answer"}},
			ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject},
		}
		got := adaptResponseFormat(in)
		last := got.Messages[len(got.Messages)-1]
		if !strings.Contains(strings.ToLower(last.Content), "json") {
			t.Errorf("expected 'json' keyword injected, got %q", last.Content)
		}
	})
}
