package tui

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestBuiltinThemesCoverEverySemanticUIRole(t *testing.T) {
	themes, err := theme.LoadBuiltins()
	if err != nil {
		t.Fatalf("load built-in themes: %v", err)
	}
	if len(themes) == 0 {
		t.Fatal("no built-in themes")
	}
	for _, candidate := range themes {
		if !semanticThemeComplete(candidate) {
			t.Errorf("theme %q is missing one or more semantic UI roles", candidate.Name)
		}
	}
}
