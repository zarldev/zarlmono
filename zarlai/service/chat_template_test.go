package service_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
)

func TestGemma4Template_PrependsSentinelWhenReasoningEnabled(t *testing.T) {
	tmpl := service.Gemma4Template{}
	msgs := []service.Message{
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

func TestGemma4Template_NoSentinelWhenReasoningDisabled(t *testing.T) {
	tmpl := service.Gemma4Template{}
	msgs := []service.Message{
		{Role: "system", Content: "You are zarl."},
	}

	got := tmpl.ShapeMessages(msgs, false)

	if got[0].Content != "You are zarl." {
		t.Errorf("system content mutated: %q", got[0].Content)
	}
	if len(got) > 0 && len(msgs) > 0 && &got[0] != &msgs[0] {
		t.Errorf("expected no-op to return input slice; got copy")
	}
}

func TestGemma4Template_InsertsSystemWhenMissing(t *testing.T) {
	tmpl := service.Gemma4Template{}
	msgs := []service.Message{{Role: "user", Content: "hi"}}

	got := tmpl.ShapeMessages(msgs, true)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Role != "system" || got[0].Content != "<|think|>" {
		t.Errorf("got[0] = %+v, want system <|think|>", got[0])
	}
}

func TestGemma4Template_ThinkingKwargs(t *testing.T) {
	tmpl := service.Gemma4Template{}

	on := tmpl.ThinkingKwargs(true)
	if !on.EnableThinking {
		t.Errorf("EnableThinking = false, want true")
	}

	off := tmpl.ThinkingKwargs(false)
	if off.EnableThinking {
		t.Errorf("EnableThinking = true, want false")
	}
}

func TestQwen3Template_DoesNotMutateMessages(t *testing.T) {
	tmpl := service.Qwen3Template{}
	msgs := []service.Message{
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

func TestQwen3Template_ThinkingKwargsMirrorReasoning(t *testing.T) {
	tmpl := service.Qwen3Template{}

	if got := tmpl.ThinkingKwargs(true); !got.EnableThinking {
		t.Errorf("reasoning=true: EnableThinking = false, want true")
	}
	if got := tmpl.ThinkingKwargs(false); got.EnableThinking {
		t.Errorf("reasoning=false: EnableThinking = true, want false")
	}
}
