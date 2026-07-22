package compact_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func handoverHistory() []llm.Message {
	// A realistically large history — handover only fires under context
	// pressure, where the older content dwarfs the produced document.
	bulk := strings.Repeat("work log line describing what happened. ", 200)
	return []llm.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "add a feature"},
		{Role: "assistant", Content: bulk},
		{Role: "user", Content: "and tests"},
		{Role: "assistant", Content: bulk},
	}
}

// Handover collapses the whole conversation to [system] + [one user seed],
// folds in the plan section, invokes the writer, and reports the byte saving.
func TestHandover_ClearsAndReseeds(t *testing.T) {
	t.Parallel()
	state := &fakeStateProvider{plan: []compact.PlanStep{
		{Title: "scaffold", Status: "completed"},
		{Title: "wire it", Status: "in_progress"},
	}}
	prov := execFakeProvider{body: "## Objective\nShip the feature.\n## Next steps\nWrite tests."}

	var wrote string
	writer := func(_ context.Context, doc string) (string, error) {
		wrote = doc
		return "/ws/.zarlcode/handovers/2026-01-01-000000.md", nil
	}
	h := compact.NewHandover(prov, "fake-model", state, writer)

	res, err := h.Compact(t.Context(), handoverHistory(), 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Engine != compact.EngineHandover {
		t.Fatalf("engine = %q, want handover", res.Engine)
	}
	// Only the leading system message plus a single user seed survive.
	if len(res.History) != 2 {
		t.Fatalf("history len = %d, want 2 (system + seed)", len(res.History))
	}
	if res.History[0].Role != llm.RoleSystem {
		t.Fatalf("msg[0] role = %q, want system", res.History[0].Role)
	}
	seed := res.History[1]
	if seed.Role != llm.RoleUser {
		t.Fatalf("seed role = %q, want user", seed.Role)
	}
	for _, want := range []string{"# Session handover", "## Objective", "PLAN PROGRESS", "Handover saved to /ws/.zarlcode/handovers/"} {
		if !strings.Contains(seed.Content, want) {
			t.Fatalf("seed missing %q in:\n%s", want, seed.Content)
		}
	}
	// The writer received the composed document (plan + body), sans the seed
	// header and saved-note wrapper.
	if !strings.Contains(wrote, "## Objective") || !strings.Contains(wrote, "PLAN PROGRESS") {
		t.Fatalf("writer got unexpected doc:\n%s", wrote)
	}
	if strings.Contains(wrote, "Handover saved to") {
		t.Fatal("writer should receive the doc without the saved-note wrapper")
	}
	if res.BytesTrimmed <= 0 {
		t.Fatalf("BytesTrimmed = %d, want positive", res.BytesTrimmed)
	}
}

// A nil writer still reseeds; the document just isn't persisted.
func TestHandover_NoWriter(t *testing.T) {
	t.Parallel()
	prov := execFakeProvider{body: "## Objective\nx"}
	h := compact.NewHandover(prov, "m", nil, nil)
	res, err := h.Compact(t.Context(), handoverHistory(), 0)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.History) != 2 {
		t.Fatalf("history len = %d, want 2", len(res.History))
	}
	if strings.Contains(res.History[1].Content, "Handover saved to") {
		t.Fatal("no writer → no saved-to note")
	}
}

func TestHandover_NilProvider(t *testing.T) {
	t.Parallel()
	h := compact.NewHandover(nil, "m", nil, nil)
	if _, err := h.Compact(t.Context(), handoverHistory(), 0); err == nil {
		t.Fatal("nil provider should error")
	}
}

func TestHandover_EmptyDocErrors(t *testing.T) {
	t.Parallel()
	h := compact.NewHandover(execFakeProvider{body: "   "}, "m", nil, nil)
	if _, err := h.Compact(t.Context(), handoverHistory(), 0); err == nil {
		t.Fatal("empty model output should error")
	}
}

// Provider failures surface as errors, not a silent unchanged history.
func TestHandover_ProviderFailure(t *testing.T) {
	t.Parallel()
	h := compact.NewHandover(execFakeProvider{err: errors.New("boom")}, "m", nil, nil)
	if _, err := h.Compact(t.Context(), handoverHistory(), 0); err == nil {
		t.Fatal("provider failure should error")
	}
}

// Nothing but system messages: no work to hand over, history returned intact.
func TestHandover_OnlySystem(t *testing.T) {
	t.Parallel()
	h := compact.NewHandover(execFakeProvider{body: "x"}, "m", nil, nil)
	hist := []llm.Message{{Role: "system", Content: "sys"}}
	res, err := h.Compact(t.Context(), hist, 0)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(res.History) != 1 {
		t.Fatalf("history len = %d, want 1 (unchanged)", len(res.History))
	}
}
