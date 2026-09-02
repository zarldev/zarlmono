package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestExpandedDiffRendersCountsContentAndWraps(t *testing.T) {
	long := "+" + strings.Repeat("x", 200)
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	model, _ = model.Update(teasink.DiffMsg{TaskID: "turn", Path: "foo.go", Diff: "@@ foo.go @@\n keep\n-old line\n+new line\n" + long})

	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	out := ansi.Strip(model.View().Content)

	for _, want := range []string{"± foo.go", "+2", "-1", "keep", "-old line", "+new line"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded diff missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "@@ foo.go @@") {
		t.Errorf("expanded diff retained redundant file header:\n%s", out)
	}
	for i, line := range strings.Split(out, "\n") {
		if width := ansi.StringWidth(line); width > 80 {
			t.Errorf("line %d width %d exceeds viewport: %q", i, width, line)
		}
	}
}
