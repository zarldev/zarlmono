package tui

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// The themepicker renders through the shared drawDialogPane: framed box with
// the "themes" title, a summary/list body, and footer key hints.
func TestThemePicker_RendersFramedBox(t *testing.T) {
	UseTheme(theme.Theme{Name: "nord"})
	defer UseTheme(theme.Theme{})

	p := newThemePicker()
	scr := uv.NewScreenBuffer(120, 40)
	p.draw(scr, uv.Rect(0, 0, 120, 40))
	out := ansi.Strip(scr.Render())

	// Title + summary + footer, plus the selected theme ("nord") which must stay
	// visible even when scrolled, and the full footer ("close") which the box now
	// widens to fit.
	for _, want := range []string{"themes", "previewing live", "nord", "navigate", "select", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("theme picker render missing %q:\n%s", want, out)
		}
	}
}

// The model quick-pick renders through drawDialogPane: provider tabs in the
// context row, models in the body, key hints in the footer.
func TestModelQuickPick_RendersFramedBox(t *testing.T) {
	p := newModelQuickPick(
		[]string{"openai", "anthropic"},
		map[string][]string{"openai": {"gpt-5.5", "gpt-5-mini"}, "anthropic": {"claude-opus"}},
		"openai", "gpt-5.5", func(string, string) {},
	)
	scr := uv.NewScreenBuffer(120, 40)
	p.draw(scr, uv.Rect(0, 0, 120, 40))
	out := ansi.Strip(scr.Render())

	// Frame + tabs (context) + a body list item + footer hints.
	for _, want := range []string{"model", "openai", "anthropic", "gpt-5.5", "navigate", "provider", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("model picker render missing %q:\n%s", want, out)
		}
	}
}

// The selected model stays visible when scrolled to the bottom of a long list —
// the same listWindow fix as the theme picker (the ↑ more indicator must not
// push the cursor off-screen onto the footer).
func TestModelQuickPick_SelectedVisibleWhenScrolled(t *testing.T) {
	models := make([]string, 0, 30)
	for i := range 30 {
		models = append(models, fmt.Sprintf("model-%02d", i))
	}
	p := newModelQuickPick(
		[]string{"openai"},
		map[string][]string{"openai": models},
		"openai", "model-00", func(string, string) {},
	)
	p.cursor = len(models) - 1 // select the last model

	scr := uv.NewScreenBuffer(120, 30)
	p.draw(scr, uv.Rect(0, 0, 120, 30))
	out := ansi.Strip(scr.Render())

	if !strings.Contains(out, "▸ model-29") {
		t.Errorf("selected model must stay visible when scrolled to the bottom:\n%s", out)
	}
}
