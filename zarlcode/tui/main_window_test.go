package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestMainWindowStatePaneShowsSessionName(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	model, _ = model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'n'})
	for _, r := range "Fix Ghostty notifications" {
		model, _ = model.Update(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "name      Fix Ghostty notifications") {
		t.Fatalf("state pane missing session name:\n%s", out)
	}
}

func TestConfirmedConversationActionDismissesDialogStack(t *testing.T) {
	tests := []struct {
		name string
		keys []tea.KeyPressMsg
	}{
		{
			name: "compact",
			keys: []tea.KeyPressMsg{
				{Mod: tea.ModCtrl, Code: 'q'},
				{Text: "c", Code: 'c'},
			},
		},
		{
			name: "clear",
			keys: []tea.KeyPressMsg{
				{Mod: tea.ModCtrl, Code: 'q'},
				{Text: "x", Code: 'x'},
				{Text: "y", Code: 'y'},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var model tea.Model = tui.New()
			model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			for _, key := range tt.keys {
				model, _ = model.Update(key)
			}

			out := ansi.Strip(model.View().Content)
			if strings.Contains(out, "conversation context") {
				t.Fatalf("conversation dialog remained after confirmation:\n%s", out)
			}
		})
	}
}
