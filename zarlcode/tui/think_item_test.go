package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestThinkingItem_ExpandedRendersMarkdown(t *testing.T) {
	tui.UseTheme(theme.Theme{Muted: "#999999"})
	defer tui.UseTheme(theme.Theme{})

	raw := strings.Join(tui.RenderThinking(80, "```go\nx := 1\n```"), "\n")
	plain := ansi.Strip(raw)
	if strings.Contains(plain, "```") {
		t.Fatalf("expanded thinking should render markdown, not raw fences:\n%s", plain)
	}
	if !strings.Contains(plain, "x := 1") {
		t.Fatalf("expanded thinking lost code body:\n%s", plain)
	}
	if !strings.Contains(raw, theme.Color("#999999").FG()) {
		t.Fatalf("expanded thinking body should render muted:\n%q", raw)
	}
}

func TestThinkingItem_NormalizesAdjacentBoldChunks(t *testing.T) {
	plain := ansi.Strip(strings.Join(tui.RenderThinking(80, "**Planning removal****Refactoring queued indicator**"), "\n"))
	if strings.Contains(plain, "****") {
		t.Fatalf("expanded thinking leaked adjacent markdown markers:\n%s", plain)
	}
	if !strings.Contains(plain, "Planning removal") || !strings.Contains(plain, "Refactoring queued indicator") {
		t.Fatalf("expanded thinking lost chunk headings:\n%s", plain)
	}
}
