package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func drive(t *testing.T, msgs ...tea.Msg) string {
	t.Helper()
	var m tea.Model = tui.New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return ansi.Strip(m.View().Content)
}
