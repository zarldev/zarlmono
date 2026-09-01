package backends_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// The Vertex builtin's public contract: it is present in the catalogue,
// routes through the Vertex adapter type, uses ADC rather than an API key,
// and provides models for the picker.
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
	if len(def.SeedModels) == 0 || def.DefaultModel == "" {
		t.Fatal("vertex builtin needs seed models and a default for the picker")
	}
}
