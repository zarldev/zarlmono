package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineNavigationPreservesTranscript(t *testing.T) {
	m := tui.New()
	step(t, m, window(100, 20))
	for _, s := range []string{"one", "two", "three"} {
		step(t, m, teasink.ContentMsg{TaskID: "t", Delta: s + "\n"})
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "one") {
		t.Fatalf("navigation lost transcript:\n%s", out)
	}
}
