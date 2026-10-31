package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestANSIChromeRetainsLiveWorkingInformation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer tui.UseTheme(theme.Theme{})
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		ui.Update(teasink.ConversationStartedMsg{TaskID: "live", Prompt: "preserve the working information"})
		time.Sleep(36*time.Minute + 37*time.Second)
		usage := &llm.Usage{CompletionTokens: 23 * 2197}
		ui.Update(teasink.IterationCompletedMsg{TaskID: "live", Iter: 1, Usage: usage, Delta: usage})
		ui.Update(tea.PasteMsg{Content: "important information"})
		for _, name := range []string{"ansi-bbs", "retro-amber", "terminal-green"} {
			palette, ok := theme.ByName(name)
			if !ok {
				t.Fatalf("missing palette %q", name)
			}
			tui.UseTheme(palette)
			view := ui.View().Content
			plain := ansi.Strip(view)
			lines := strings.Split(plain, "\n")
			for _, want := range []string{"working", "23 tok/s", "36:37"} {
				if !strings.Contains(lines[27], want) {
					t.Fatalf("%s lost %q from activity: %q", name, want, lines[27])
				}
			}
			if !strings.HasPrefix(lines[27], "─ ") || !strings.HasSuffix(lines[27], "─") || !strings.Contains(lines[28], "› important information") {
				t.Fatalf("live row or prompt geometry changed: %q / %q", lines[27], lines[28])
			}
			if !strings.ContainsAny(lines[27], "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
				t.Fatalf("working spinner lost: %q", lines[27])
			}
			if dir := os.Getenv("ZARL_CHROME_SNAPSHOTS"); dir != "" && name == "ansi-bbs" {
				if err := os.WriteFile(filepath.Join(dir, "code-working.ansi"), []byte(view), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
