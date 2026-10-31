package chrome_test

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestLongMessageDoesNotColorIndicatorLane(t *testing.T) {
	palette, _ := theme.ByName("ansi-bbs")
	style := chrome.Style{Theme: palette}
	screen := uv.NewScreenBuffer(20, 1)
	uv.NewStyledString(style.Status(palette.Error.On(strings.Repeat("error", 40)), "o", 20)).Draw(screen, screen.Bounds())
	last := screen.CellAt(19, 0)
	if last.Content != "o" || last.Style.Fg != nil || last.Style.Bg != nil {
		t.Fatalf("message styling leaked into indicator: %#v", last)
	}
}
