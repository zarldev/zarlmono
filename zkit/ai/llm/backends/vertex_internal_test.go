package backends

import "testing"

// The Vertex builtin's whole contract: present in the catalogue, routed
// to the googleVertex adapter, registered in the adapter table, and —
// because Vertex authenticates via ADC, not keys — offering no API key
// field in the UI.
func TestVertexBuiltinWiring(t *testing.T) {
	t.Parallel()
	def, ok := Builtin("google-vertex")
	if !ok {
		t.Fatal("google-vertex missing from BuiltinDefinitions")
	}
	if def.AdapterType != AdapterTypes.GOOGLEVERTEX {
		t.Fatalf("adapter type = %v, want GOOGLEVERTEX", def.AdapterType)
	}
	if at := adapterDiscriminator(def.AdapterType); at != googleVertex {
		t.Fatalf("discriminator = %v, want googleVertex", at)
	}
	if _, ok := lookupAdapter(googleVertex); !ok {
		t.Fatal("googleVertex absent from the adapter table")
	}
	if def.UsesAPIKey() {
		t.Fatal("vertex must not offer an API key field — ADC only")
	}
	if len(def.SeedModels) == 0 || def.DefaultModel == "" {
		t.Fatal("vertex builtin needs seed models and a default for the picker")
	}
}
