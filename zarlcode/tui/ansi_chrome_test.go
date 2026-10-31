package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestANSIWorkingViewGeometryAndThemeChanges(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.SetWorkspace("/demo", "demo-model")
	ui.Update(teasink.ConversationStartedMsg{TaskID: "demo", Prompt: "make the terminal feel like home"})
	ui.Update(teasink.ContentMsg{TaskID: "demo", Delta: "a shared visual language, with your choice of palette."})
	ui.Update(teasink.ConversationEndedMsg{TaskID: "demo", Reason: runner.TerminalCompleted})
	ui.Update(tea.PasteMsg{Content: "keep the images and theme picker"})
	for _, size := range [][2]int{{40, 14}, {80, 24}, {100, 30}, {160, 45}} {
		ui.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, name := range []string{"ansi-bbs", "retro-amber", "terminal-green", "catppuccin-mocha"} {
			palette, ok := theme.ByName(name)
			if !ok {
				t.Fatalf("missing catalogue theme %q", name)
			}
			tui.UseTheme(palette)
			view := ui.View().Content
			plain := ansi.Strip(view)
			for _, line := range strings.Split(plain, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("%s %v overflow: %q", name, size, line)
				}
			}
			if !strings.Contains(plain, "demo-model") || !strings.Contains(plain, "keep the images") {
				t.Fatalf("content lost in %s %v: %s", name, size, plain)
			}
			if dir := os.Getenv("ZARL_CHROME_SNAPSHOTS"); dir != "" && size[0] == 100 && name == "catppuccin-mocha" {
				if err := os.WriteFile(filepath.Join(dir, "code.ansi"), []byte(view), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
