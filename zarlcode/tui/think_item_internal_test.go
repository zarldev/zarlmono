package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestThinkingItem_ExpandedRendersMarkdown(t *testing.T) {
	UseTheme(theme.Theme{Muted: "#999999"})
	defer UseTheme(theme.Theme{})

	it := &thinkingItem{
		text:     "```go\nx := 1\n```",
		expanded: true,
		done:     true,
	}

	raw := strings.Join(it.render(80), "\n")
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
