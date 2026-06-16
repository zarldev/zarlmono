package tui

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestDrawSplitPaneRendersSharedChrome(t *testing.T) {
	buf := uv.NewScreenBuffer(80, 20)
	layout, ok := drawSplitPane(buf, buf.Bounds(), "files", 24)
	if !ok {
		t.Fatal("drawSplitPane should lay out a normal terminal-sized area")
	}
	if layout.Context.Empty() || layout.Nav.Empty() || layout.Detail.Empty() || layout.Footer.Empty() {
		t.Fatalf("expected named split-pane regions, got %#v", layout)
	}

	drawPaneRow(buf, layout.Context, " /workspace", "esc close ")
	drawPaneRow(buf, layout.Footer, " ↑↓ navigate", "enter open ")
	out := buf.Render()
	for _, want := range []string{"files", "/workspace", "esc close", "↑↓ navigate", "┬", "┴", "│"} {
		if !strings.Contains(out, want) {
			t.Errorf("shared split-pane chrome missing %q in:\n%s", want, out)
		}
	}
}

func TestDrawFrameUsesThemeColors(t *testing.T) {
	UseTheme(theme.Theme{Border: "#123456", Primary: "#abcdef"})
	t.Cleanup(func() { UseTheme(theme.Theme{}) })

	buf := uv.NewScreenBuffer(24, 5)
	drawFrame(buf, buf.Bounds(), defaultFrameStyle("demo"))
	out := buf.Render()
	if !strings.Contains(out, theme.Color("#123456").FG()+"┌") {
		t.Fatalf("frame border did not use theme border color:\n%q", out)
	}
	if !strings.Contains(out, theme.Color("#abcdef").FG()+"demo") {
		t.Fatalf("frame title did not use theme primary color:\n%q", out)
	}
}

func TestCorePanesUseUnifiedThemedFrame(t *testing.T) {
	UseTheme(theme.Theme{Border: "#224466", Primary: "#cc8844", Muted: "#999999", Success: "#00aa00"})
	t.Cleanup(func() { UseTheme(theme.Theme{}) })

	for _, tc := range []struct {
		name string
		draw func(*UI, uv.ScreenBuffer)
	}{
		{name: "timeline", draw: func(m *UI, buf uv.ScreenBuffer) { m.drawTimeline(buf, buf.Bounds()) }},
		{name: "sidebar", draw: func(m *UI, buf uv.ScreenBuffer) { m.drawSidebar(buf, buf.Bounds()) }},
		{name: "dialog", draw: func(_ *UI, buf uv.ScreenBuffer) { drawDialogBox(buf, buf.Bounds(), "keys", []string{"enter  select"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			buf := uv.NewScreenBuffer(80, 20)
			tc.draw(m, buf)
			out := buf.Render()
			if !strings.Contains(out, theme.Color("#224466").FG()) {
				t.Fatalf("%s did not render themed border color:\n%q", tc.name, out)
			}
		})
	}
}
