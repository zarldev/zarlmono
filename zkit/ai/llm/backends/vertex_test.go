package backends_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// TestVertexBuiltinWiring verifies the Vertex route uses ADC, retains an
// explicit default, and does not borrow the Gemini API fallback catalogue.
func TestVertexBuiltinWiring(t *testing.T) {
	t.Parallel()

	def, ok := backends.Builtin("google-vertex")
	if !ok {
		t.Fatal("google-vertex missing from BuiltinDefinitions")
	}
	if def.AdapterType != backends.AdapterTypes.GOOGLEVERTEX {
		t.Fatalf("adapter type = %v, want GOOGLEVERTEX", def.AdapterType)
	}
	if def.UsesAPIKey() {
		t.Fatal("vertex must not offer an API key field — ADC only")
	}
	if def.DefaultModel == "" {
		t.Fatal("vertex builtin needs a default model")
	}
	if len(def.SeedModels) != 0 {
		t.Fatalf("vertex seed models = %v, want no cross-surface fallback catalogue", def.SeedModels)
	}
}
