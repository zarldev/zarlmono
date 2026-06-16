package templates_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
)

func TestInferFamily_KnownModels(t *testing.T) {
	cases := []struct {
		model string
		want  templates.Family
	}{
		{"qwen3.6-35b-a3b", templates.Qwen},
		{"Qwen3.6-35B-A3B", templates.Qwen}, // case-insensitive
		{"qwen3:14b", templates.Qwen},       // Ollama-style ID
		{"gemma-4-27b", templates.Gemma},
		{"google/gemma-2-9b-it", templates.Gemma},
	}
	for _, c := range cases {
		got := templates.InferFamily(c.model)
		if got.Name() != c.want.Name() {
			t.Errorf("InferFamily(%q) = %s, want %s", c.model, got.Name(), c.want.Name())
		}
	}
}

func TestInferFamily_FallsBackToQwen(t *testing.T) {
	// No hint matches; should land on the configured fallback (Qwen)
	// rather than zero-value or panicking.
	got := templates.InferFamily("acme/totally-unknown-model")
	if got.Name() != templates.Qwen.Name() {
		t.Errorf("unknown model should fall back to Qwen, got %s", got.Name())
	}
	if got.IsZero() {
		t.Error("InferFamily must never return a zero Family")
	}
}

func TestPickByModel_Equivalence(t *testing.T) {
	// PickByModel is sugar for InferFamily(...).Template(); confirm
	// the equivalence so removing one in favor of the other is safe.
	for _, model := range []string{"qwen3-coder", "gemma-4-27b", "unknown-x"} {
		direct := templates.PickByModel(model)
		viaInfer := templates.InferFamily(model).Template()
		// Two zero-size structs of the same type are == comparable.
		if direct != viaInfer {
			t.Errorf("PickByModel(%q) diverged from InferFamily.Template()", model)
		}
	}
}

func TestFamily_Template(t *testing.T) {
	if templates.Qwen.Template() == nil {
		t.Error("Qwen.Template() returned nil")
	}
	if templates.Gemma.Template() == nil {
		t.Error("Gemma.Template() returned nil")
	}
}

func TestZeroFamily(t *testing.T) {
	var f templates.Family
	if !f.IsZero() {
		t.Error("zero Family should report IsZero")
	}
	if got := templates.Qwen.IsZero(); got {
		t.Error("Qwen should not be zero")
	}
}

func TestAll_ReturnsCopy(t *testing.T) {
	all := templates.All()
	if len(all) < 2 {
		t.Fatalf("All() = %d entries, want >=2", len(all))
	}
	all[0] = templates.Family{}
	again := templates.All()
	if again[0].IsZero() {
		t.Error("All() returned an aliased slice — mutation leaked")
	}
}
