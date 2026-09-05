package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestFreshAndClearReplaceSessionOwnedState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset func(*tui.UI)
	}{
		{name: "fresh", reset: func(ui *tui.UI) { ui.StartFreshSession("") }},
		{name: "clear", reset: func(ui *tui.UI) { ui.Submit("/clear") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			ui.SeedSessionOwnedState()
			if ui.SessionOwnedStateEmpty() {
				t.Fatal("test did not seed session-owned state")
			}
			tc.reset(ui)
			if !ui.SessionOwnedStateEmpty() {
				t.Fatal("session-owned state survived replacement")
			}
		})
	}
}
