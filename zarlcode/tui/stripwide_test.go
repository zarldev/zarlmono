package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTranscriptFallbackStripsWideGraphemes(t *testing.T) {
	ui := tui.New()
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleAssistant, Content: "🌤️ Partly Cloudy\ndone 🎉\n• 19°C\n▌ ✓ ✗ │ ◌ ▎"}})
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := ansi.Strip(model.View().Content)

	for _, absent := range []string{"🌤", "️", "🎉"} {
		if strings.Contains(out, absent) {
			t.Errorf("wide grapheme %q survived fallback rendering:\n%s", absent, out)
		}
	}
	for _, want := range []string{"Partly Cloudy", "done", "• 19°C", "▌ ✓ ✗ │ ◌ ▎"} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback rendering missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if w, n := ansi.StringWidth(line), len([]rune(line)); w != n {
			t.Errorf("fallback line %q: width %d != rune count %d", line, w, n)
		}
	}
}
