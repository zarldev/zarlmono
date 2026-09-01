package tui_test

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
		roles := []theme.Color{
			candidate.Bg, candidate.Fg, candidate.Primary, candidate.Secondary,
			candidate.User, candidate.Assistant, candidate.Tool, candidate.Success,
			candidate.Error, candidate.Warning, candidate.Muted, candidate.Subtle,
			candidate.Border, candidate.BorderFocus, candidate.Highlight,
			candidate.Info, candidate.PlanMode,
		}
		for i, role := range roles {
			if role == "" {
				t.Errorf("theme %q semantic role %d is empty", candidate.Name, i)
			}
		}
	}
}
