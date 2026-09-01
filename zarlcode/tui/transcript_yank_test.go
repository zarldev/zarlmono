package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTranscriptBrowseVisualSelectionYanksText(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "turn-1", Prompt: "copy this transcript line"})

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	model, cmd := model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})

	if cmd == nil {
		t.Fatal("y should return clipboard and toast commands")
	}
	if out := ansi.Strip(model.View().Content); !strings.Contains(out, "copied 1 lines") {
		t.Fatalf("transcript yank did not report copied selection:\n%s", out)
	}
}

func TestTranscriptBrowseStillAcceptsPromptTextWhileScrolled(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "turn-1", Prompt: "scrollback"})

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})

	out := model.View().Content
	if !strings.Contains(out, "x▏") && !strings.Contains(out, "x█") {
		t.Fatalf("typing while transcript is scrolled did not reach composer:\n%s", out)
	}
	if !strings.Contains(out, "browse 100%") {
		t.Fatalf("typing while scrolled unexpectedly exited transcript browse mode:\n%s", out)
	}
}
