package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAssistantTranscriptRendersMarkdown(t *testing.T) {
	ui := tui.New()
	ui.RestoreTranscript([]llm.Message{{
		Role:    llm.RoleAssistant,
		Content: "## Section\n\nsome **bold** words in a paragraph",
	}})
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := ansi.Strip(model.View().Content)
	for _, want := range []string{"Section", "bold", "paragraph"} {
		if !strings.Contains(out, want) {
			t.Errorf("assistant Markdown render missing %q:\n%s", want, out)
		}
	}
}
