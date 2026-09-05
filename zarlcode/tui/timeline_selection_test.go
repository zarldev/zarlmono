package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineSelectionModeIsVisible(t *testing.T) {
	m := tui.New()
	step(t, m, window(100, 20))
	step(t, m, teasink.ContentMsg{TaskID: "t", Delta: "select me"})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "select me") {
		t.Fatalf("selection hid content:\n%s", out)
	}
}
