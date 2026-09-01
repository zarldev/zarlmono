package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestCockpitRendersSessionOverview(t *testing.T) {
	ui := tui.New()
	ui.SetWorkspace("/tmp/project", "test-model")
	ui.SetProvider("openai")
	got := strings.Join(ui.RenderCockpitLines(56), "\n")
	for _, want := range []string{"provider", "model", "workspace", "openai", "test-model"} {
		if !strings.Contains(got, want) {
			t.Errorf("cockpit missing %q:\n%s", want, got)
		}
	}
}
