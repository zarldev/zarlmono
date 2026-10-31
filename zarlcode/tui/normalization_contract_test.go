package tui_test

import (
	"fmt"
	"html"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func normalizationCell(t *testing.T, canvas uv.ScreenBuffer, text string, row int) *uv.Cell {
	t.Helper()
	line := transcriptScreenRow(canvas, row)
	at := strings.Index(line, text)
	if at < 0 {
		t.Fatalf("missing %q in row %d: %q", text, row, line)
	}
	return canvas.CellAt(ansi.StringWidth(line[:at]), row)
}

func TestFamilySelectionSurvivesSettingsFocusAndThemeChanges(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("NO_COLOR", "")
	t.Setenv("ZARLCODE_ASCII", "")
	defer tui.UseTheme(theme.Theme{})
	for _, size := range [][2]int{{40, 14}, {80, 24}, {100, 30}, {160, 45}} {
		for _, name := range []string{"ansi-bbs", "retro-amber", "terminal-green"} {
			palette, ok := theme.ByName(name)
			if !ok {
				t.Fatalf("missing theme %q", name)
			}
			// Deliberately distinguish these roles so legacy Highlight cannot pass.
			palette.Highlight = "#652143"
			m := newSettingsLayoutUI(t, size[0], size[1])
			tui.UseTheme(palette)
			fg, bg := (chrome.Style{Theme: palette}).SelectionColors()
			check := func() uv.ScreenBuffer {
				t.Helper()
				canvas := surfaceCanvas(m, size[0], size[1])
				for _, x := range []int{1, min(5, size[0]/3-2)} {
					cell := canvas.CellAt(x, 1)
					if !sameSurface(cell.Style.Bg, lg.Color(string(bg))) || !sameSurface(cell.Style.Fg, lg.Color(string(fg))) {
						t.Fatalf("%s %v selected category uses a different family surface at %d: %+v", name, size, x, cell.Style)
					}
				}
				return canvas
			}
			before := check()
			if cell := normalizationCell(t, before, "settings", 0); !sameSurface(cell.Style.Fg, lg.Color(string(palette.Primary))) {
				t.Fatal("focused pane heading lacks the family focus role")
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
			after := check()
			if cell := normalizationCell(t, after, "settings", 0); !sameSurface(cell.Style.Fg, lg.Color(string(palette.Subtle))) {
				t.Fatal("selection and keyboard focus are conflated")
			}
			writeNormalizationGallery(t, after, name, "settings", size)
		}
	}
}

func TestFamilyTranscriptSelectionUsesSameColorsWithoutShiftingText(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("ZARLCODE_ASCII", "")
	defer tui.UseTheme(theme.Theme{})
	palette, _ := theme.ByName("ansi-bbs")
	palette.Highlight = "#652143"
	tui.UseTheme(palette)
	m := tui.New()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "chosen café 界"}})
	before := surfaceCanvas(m, 80, 24)
	row := surfaceRow(t, before, "you")
	bodyRow := surfaceRow(t, before, "chosen café")
	m.Update(tea.MouseClickMsg{X: 0, Y: row, Button: tea.MouseLeft})
	after := surfaceCanvas(m, 80, 24)
	fg, bg := (chrome.Style{Theme: palette}).SelectionColors()
	if cell := after.CellAt(1, row); cell.Content != "y" || !sameSurface(cell.Style.Bg, lg.Color(string(bg))) || !sameSurface(cell.Style.Fg, lg.Color(string(fg))) {
		t.Fatalf("transcript selection disagrees with family rows: fg=%v bg=%v, want=%s/%s, marker=%q\n%s", cell.Style.Fg, cell.Style.Bg, fg, bg, after.CellAt(0, row).Content, after.String())
	}
	if cell := after.CellAt(0, row); cell.Content != "▸" {
		t.Fatalf("browse marker = %q", cell.Content)
	}
	if before.CellAt(2, bodyRow).Content != after.CellAt(2, bodyRow).Content || m.ComposerText() != "" {
		t.Fatal("selection changed content geometry or input")
	}
	t.Setenv("ZARLCODE_ASCII", "1")
	if cell := surfaceCanvas(m, 80, 24).CellAt(0, row); cell.Content != ">" {
		t.Fatalf("ASCII browse marker = %q", cell.Content)
	}
	t.Setenv("ZARLCODE_ASCII", "")
	writeNormalizationGallery(t, after, "ansi-bbs", "transcript-selection", [2]int{80, 24})
}

func TestFamilyActiveTabsUseSelectionRole(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	defer tui.UseTheme(theme.Theme{})
	for _, name := range []string{"ansi-bbs", "retro-amber", "terminal-green"} {
		palette, _ := theme.ByName(name)
		palette.Highlight = "#652143"
		tui.UseTheme(palette)
		m := tui.New()
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
		canvas := surfaceCanvas(m, 100, 30)
		fg, bg := (chrome.Style{Theme: palette}).SelectionColors()
		cell := normalizationCell(t, canvas, "overview", 0)
		if !sameSurface(cell.Style.Bg, lg.Color(string(bg))) || !sameSurface(cell.Style.Fg, lg.Color(string(fg))) {
			t.Fatalf("%s active tab is not a family selection: %+v", name, cell.Style)
		}
		writeNormalizationGallery(t, canvas, name, "dashboard", [2]int{100, 30})
	}
}

func TestNormalizationVisualGallery(t *testing.T) {
	if os.Getenv("ZARL_NORMALIZATION_GALLERY") == "" {
		t.Skip("set ZARL_NORMALIZATION_GALLERY to generate offline comparison fixtures")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("NO_COLOR", "")
	defer tui.UseTheme(theme.Theme{})
	synctest.Test(t, func(t *testing.T) {
		for _, name := range []string{"ansi-bbs", "retro-amber", "terminal-green"} {
			palette, _ := theme.ByName(name)
			for _, size := range [][2]int{{40, 14}, {80, 24}, {100, 30}, {160, 45}} {
				for _, scene := range []string{"intro", "working", "settings", "help", "error"} {
					m := tui.New()
					m.SetWorkspace(t.TempDir(), "fixture-model")
					m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					m.AddTranscriptMessages([]llm.Message{
						{Role: llm.RoleUser, Content: "inspect received resources"},
						{Role: llm.RoleAssistant, Content: "prepared content stays readable across themes.\n\n- native images retain their quality\n- selection and focus have different roles"},
					})
					switch scene {
					case "intro":
						m.OpenIntro(t.TempDir())
					case "settings":
						m = newSettingsLayoutUI(t, size[0], size[1])
						m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
					case "help":
						m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
					case "error":
						m.SetErrorToast("resource unavailable · retry in 15s")
					case "working":
						m.Update(teasink.ConversationStartedMsg{TaskID: "fixture", Prompt: "check the shared presentation"})
						time.Sleep(2 * time.Second)
						usage := &llm.Usage{CompletionTokens: 46}
						m.Update(teasink.IterationCompletedMsg{TaskID: "fixture", Iter: 1, Usage: usage, Delta: usage})
					}
					tui.UseTheme(palette)
					m.Update(tea.PasteMsg{Content: "keep the important information"})
					canvas := surfaceCanvas(m, size[0], size[1])
					writeNormalizationGallery(t, canvas, name, scene, size)
				}
			}
		}
	})
}

func writeNormalizationGallery(t *testing.T, canvas uv.ScreenBuffer, palette, scene string, size [2]int) {
	t.Helper()
	directory := os.Getenv("ZARL_NORMALIZATION_GALLERY")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var page strings.Builder
	page.WriteString("<!doctype html><meta charset=utf-8><style>body{background:#101322;color:#e4e6ed;padding:16px}pre{font-family:'DejaVu Sans Mono',monospace;font-size:13px;line-height:18px;white-space:pre}small{color:#a4adc4}</style><small>offline cell fixture — not a terminal capture</small><pre>")
	rgb := func(c color.Color) string {
		if c == nil {
			return ""
		}
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
	for y := range canvas.Height() {
		for x := 0; x < canvas.Width(); x++ {
			cell := canvas.CellAt(x, y)
			if cell.Width == 0 {
				continue
			}
			fmt.Fprintf(&page, "<span style=\"display:inline-block;width:%dch;", cell.Width)
			if c := rgb(cell.Style.Fg); c != "" {
				fmt.Fprintf(&page, "color:%s;", c)
			}
			if c := rgb(cell.Style.Bg); c != "" {
				fmt.Fprintf(&page, "background:%s;", c)
			}
			if cell.Style.Attrs&uv.AttrBold != 0 {
				page.WriteString("font-weight:bold;")
			}
			page.WriteString("\">")
			page.WriteString(html.EscapeString(cell.Content))
			page.WriteString("</span>")
		}
		page.WriteByte('\n')
	}
	page.WriteString("</pre>")
	name := fmt.Sprintf("%s-%s-%dx%d.html", scene, palette, size[0], size[1])
	if err := os.WriteFile(filepath.Join(directory, name), []byte(page.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
