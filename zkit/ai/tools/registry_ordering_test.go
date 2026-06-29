package tools_test

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TestRegistry_EnumerationOrderIsSortedAndStable guards the byte-prefix
// stability fix: Tools(), ToolSpecs(), and Names() must return tools in
// name order regardless of registration order. The runner serialises
// Tools() straight into the request's tool list, and a non-deterministic
// (Go map) order would reshuffle the specs and miss the provider's
// prefix cache (DeepSeek / llama.cpp) on every turn.
func TestRegistry_EnumerationOrderIsSortedAndStable(t *testing.T) {
	t.Parallel()

	// Register in deliberately unsorted order. Enough entries that Go's
	// randomised map iteration would almost never hand them back sorted
	// by accident.
	names := []tools.ToolName{"write", "read", "bash", "edit", "search", "apply_patch", "list", "grep"}
	r := tools.NewRegistry()
	for _, n := range names {
		r.Register(stubToolForProvider{name: n})
	}

	want := slices.Clone(names)
	slices.Sort(want)

	t.Run("Names", func(t *testing.T) {
		t.Parallel()
		if got := r.Names(); !slices.Equal(got, want) {
			t.Errorf("Names() = %v, want sorted %v", got, want)
		}
	})

	t.Run("Tools", func(t *testing.T) {
		t.Parallel()
		var got []tools.ToolName
		for tool := range r.Tools(t.Context()) {
			got = append(got, tool.Definition().Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Tools() order = %v, want sorted %v", got, want)
		}
	})

	t.Run("ToolSpecs", func(t *testing.T) {
		t.Parallel()
		specs := r.ToolSpecs()
		got := make([]tools.ToolName, 0, len(specs))
		for _, s := range specs {
			got = append(got, s.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("ToolSpecs() order = %v, want sorted %v", got, want)
		}
	})
}
