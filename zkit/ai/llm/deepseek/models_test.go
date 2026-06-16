package deepseek_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestContextWindowFor(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat", "deepseek-reasoner"} {
		if got := deepseek.ContextWindowFor(model); got != 1_000_000 {
			t.Errorf("ContextWindowFor(%q) = %d, want 1000000", model, got)
		}
	}
	if got := deepseek.ContextWindowFor("unknown"); got != 0 {
		t.Errorf("ContextWindowFor(unknown) = %d, want 0", got)
	}
}

func TestIsReasonerModel(t *testing.T) {
	t.Parallel()

	if !deepseek.IsReasonerModel("deepseek-reasoner") {
		t.Error("IsReasonerModel(deepseek-reasoner) = false, want true")
	}
	// V4 requires reasoning_content round-tripped; chat (V3) emits none.
	// Neither should be classed as a reasoner (which would strip it).
	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat", "unknown"} {
		if deepseek.IsReasonerModel(model) {
			t.Errorf("IsReasonerModel(%q) = true, want false", model)
		}
	}
}
