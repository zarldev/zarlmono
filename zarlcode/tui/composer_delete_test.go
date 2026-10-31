package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestComposerDeleteForward(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDelete},
		{Code: tea.KeyDelete, Mod: tea.ModShift},
	} {
		t.Run(key.String(), func(t *testing.T) {
			for _, tt := range []struct {
				name   string
				text   string
				left   int
				want   string
				insert string
			}{
				{name: "empty", want: "", insert: "X"},
				{name: "end", text: "abc", want: "abc", insert: "abcX"},
				{name: "start", text: "abc", left: 3, want: "bc", insert: "Xbc"},
				{name: "middle", text: "abc", left: 2, want: "ac", insert: "aXc"},
				{name: "last", text: "abc", left: 1, want: "ab", insert: "abX"},
				{name: "unicode", text: "a🙂界b", left: 3, want: "a界b", insert: "aX界b"},
				{name: "newline", text: "a\nb", left: 2, want: "ab", insert: "aXb"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					ui := tui.New()
					step := func(msg tea.Msg) {
						model, _ := ui.Update(msg)
						ui = model.(*tui.UI)
					}
					step(tea.PasteMsg{Content: tt.text})
					for range tt.left {
						step(tea.KeyPressMsg{Code: tea.KeyLeft})
					}
					step(key)
					if got := ui.ComposerText(); got != tt.want {
						t.Fatalf("after delete = %q, want %q", got, tt.want)
					}
					step(tea.KeyPressMsg{Code: 'X', Text: "X"})
					if got := ui.ComposerText(); got != tt.insert {
						t.Fatalf("insert at preserved cursor = %q, want %q", got, tt.insert)
					}
				})
			}
		})
	}
}
