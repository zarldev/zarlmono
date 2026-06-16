package templates_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
)

func TestGemma4_PrependsSentinelWhenReasoningEnabled(t *testing.T) {
	t.Parallel()

	tmpl := templates.Gemma4{}
	msgs := []llm.Message{
		{Role: "system", Content: "You are zarl."},
		{Role: "user", Content: "hi"},
	}

	got := tmpl.ShapeMessages(msgs, true)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	wantSystem := "<|think|>\nYou are zarl."
	if got[0].Content != wantSystem {
		t.Errorf("system[0] = %q, want %q", got[0].Content, wantSystem)
	}
	if got[1].Content != "hi" {
		t.Errorf("user[1] = %q, want %q", got[1].Content, "hi")
	}
	if msgs[0].Content != "You are zarl." {
		t.Errorf("input msgs[0] mutated: %q", msgs[0].Content)
	}
}

func TestGemma4_NoSentinelWhenReasoningDisabled(t *testing.T) {
	t.Parallel()

	tmpl := templates.Gemma4{}
	msgs := []llm.Message{
		{Role: "system", Content: "You are zarl."},
	}

	got := tmpl.ShapeMessages(msgs, false)

	if got[0].Content != "You are zarl." {
		t.Errorf("system content mutated: %q", got[0].Content)
	}
}

func TestGemma4_InsertsSystemWhenMissing(t *testing.T) {
	t.Parallel()

	tmpl := templates.Gemma4{}
	msgs := []llm.Message{{Role: "user", Content: "hi"}}

	got := tmpl.ShapeMessages(msgs, true)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Role != "system" || got[0].Content != "<|think|>" {
		t.Errorf("got[0] = %+v, want system <|think|>", got[0])
	}
}

func TestGemma4_ThinkingKwargs(t *testing.T) {
	t.Parallel()

	tmpl := templates.Gemma4{}

	if on := tmpl.ThinkingKwargs(true); !on.EnableThinking {
		t.Errorf("EnableThinking = false, want true")
	}
	if off := tmpl.ThinkingKwargs(false); off.EnableThinking {
		t.Errorf("EnableThinking = true, want false")
	}
}

func TestQwen3_DoesNotMutateMessages(t *testing.T) {
	t.Parallel()

	tmpl := templates.Qwen3{}
	msgs := []llm.Message{
		{Role: "system", Content: "You are zarl."},
		{Role: "user", Content: "hi"},
	}

	got := tmpl.ShapeMessages(msgs, true)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Content != "You are zarl." {
		t.Errorf("system mutated: %q", got[0].Content)
	}
	if got[1].Content != "hi" {
		t.Errorf("user mutated: %q", got[1].Content)
	}
}

func TestQwen3_ThinkingKwargsMirrorReasoning(t *testing.T) {
	t.Parallel()

	tmpl := templates.Qwen3{}

	if got := tmpl.ThinkingKwargs(true); !got.EnableThinking {
		t.Errorf("reasoning=true: EnableThinking = false, want true")
	}
	if got := tmpl.ThinkingKwargs(false); got.EnableThinking {
		t.Errorf("reasoning=false: EnableThinking = true, want false")
	}
}
