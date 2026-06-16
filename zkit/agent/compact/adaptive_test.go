package compact_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAdaptiveKeepRecent_WithinBudget(t *testing.T) {
	t.Parallel()
	// Each message is 400 chars = 100 tokens. Budget 500 tokens
	// should fit exactly 5 messages.
	body := strings.Repeat("x", 400)
	history := make([]llm.Message, 10)
	for i := range history {
		history[i] = llm.Message{Role: "assistant", Content: body}
	}
	got := compact.AdaptiveKeepRecent(history, 500, 0, 100)
	if got != 5 {
		t.Errorf("kept = %d, want 5", got)
	}
}

func TestAdaptiveKeepRecent_MinKeepClamp(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 4000) // 1000 tokens each
	history := []llm.Message{
		{Role: "assistant", Content: body},
		{Role: "assistant", Content: body},
		{Role: "assistant", Content: body},
	}
	// Tiny budget (100 tok) would normally keep just 1 — the most-
	// recent message always squeaks in by the "first always counts"
	// rule. minKeep=2 lifts that to 2.
	got := compact.AdaptiveKeepRecent(history, 100, 2, 10)
	if got != 2 {
		t.Errorf("kept = %d, want 2 (minKeep clamp)", got)
	}
}

func TestAdaptiveKeepRecent_MaxKeepClamp(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 10) // tiny — well within any budget
	history := make([]llm.Message, 50)
	for i := range history {
		history[i] = llm.Message{Role: "user", Content: body}
	}
	got := compact.AdaptiveKeepRecent(history, 100000, 1, 8)
	if got != 8 {
		t.Errorf("kept = %d, want 8 (maxKeep clamp)", got)
	}
}

func TestAdaptiveKeepRecent_EmptyHistory(t *testing.T) {
	t.Parallel()
	got := compact.AdaptiveKeepRecent(nil, 1000, 0, 10)
	if got != 0 {
		t.Errorf("kept = %d, want 0 for empty history", got)
	}
}

func TestAdaptiveKeepRecent_HistoryShorterThanMaxKeep(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "user", Content: "b"},
	}
	got := compact.AdaptiveKeepRecent(history, 100000, 0, 10)
	if got != 2 {
		t.Errorf("kept = %d, want 2 (entire history)", got)
	}
}

func TestAdaptiveKeepRecent_FirstAlwaysIncluded(t *testing.T) {
	t.Parallel()
	// One huge message + budget 0. The "first counts even if over"
	// rule means we keep 1.
	history := []llm.Message{
		{Role: "assistant", Content: strings.Repeat("x", 100000)},
	}
	got := compact.AdaptiveKeepRecent(history, 100, 0, 10)
	if got != 1 {
		t.Errorf("kept = %d, want 1 (first always squeezes in)", got)
	}
}
