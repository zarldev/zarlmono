package tui_test

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func surfaceCanvas(ui *tui.UI, width, height int) uv.ScreenBuffer {
	canvas := uv.NewScreenBuffer(width, height)
	canvas.Method = ansi.GraphemeWidth
	ui.Draw(canvas, canvas.Bounds())
	return canvas
}

func surfaceRow(t *testing.T, canvas uv.ScreenBuffer, text string) int {
	t.Helper()
	for y := range canvas.Bounds().Dy() {
		var line strings.Builder
		for x := range canvas.Bounds().Dx() {
			line.WriteString(canvas.CellAt(x, y).Content)
		}
		if strings.Contains(line.String(), text) {
			return y
		}
	}
	t.Fatalf("missing %q in screen:\n%s", text, canvas.String())
	return 0
}

func sameSurface(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func TestTranscriptSurfacesFillRowsAndRespectChrome(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, width := range []int{40, 60, 100, 220} {
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		// Mode 2027 is not necessary for these width-one combining graphemes.
		ui.AddTranscriptMessages([]llm.Message{
			{Role: llm.RoleUser, Content: "USER e\u0301 │ literal\n  kept indentation"},
			{Role: llm.RoleAssistant, Content: "**ASSISTANT** reset boundary.\n\n" + strings.Repeat("wrapped prose ", 12)},
		})
		canvas := surfaceCanvas(ui, width, 40)
		userRow := surfaceRow(t, canvas, "USER")
		agentRow := surfaceRow(t, canvas, "ASSISTANT")
		paneWidth := width
		if width >= 160 {
			paneWidth -= 56
		} else if width >= 104 {
			paneWidth -= 32
		}
		for _, tc := range []struct {
			row int
			bg  color.Color
		}{{userRow, color.RGBA{33, 41, 58, 255}}, {agentRow, color.RGBA{15, 23, 42, 255}}} {
			for x := range paneWidth - 1 {
				if got := canvas.CellAt(x, tc.row).Style.Bg; !sameSurface(got, tc.bg) {
					t.Fatalf("width %d cell (%d,%d) background = %v, want %v", width, x, tc.row, got, tc.bg)
				}
			}
			if canvas.CellAt(paneWidth-1, tc.row).Style.Bg != nil {
				t.Fatal("surface spilled into scrollbar")
			}
		}
		if canvas.CellAt(2, userRow).Content != "U" || canvas.CellAt(2, agentRow).Content != "A" {
			t.Fatal("message bodies lost their two-cell inset")
		}
		for _, label := range []string{"you", "agent"} {
			row := surfaceRow(t, canvas, label)
			if canvas.CellAt(0, row).Content != " " || canvas.CellAt(1, row).Content != label[:1] {
				t.Fatalf("%s heading must start one cell left of the body", label)
			}
		}
		for _, y := range []int{0, 1, 36, 37, 38, 39} {
			if sameSurface(canvas.CellAt(0, y).Style.Bg, color.RGBA{15, 23, 42, 255}) {
				t.Fatalf("surface spilled into chrome row %d", y)
			}
		}
		// Every wrapped prose row inherits its owner, including right-side tail cells.
		for y := agentRow + 1; y < 36; y++ {
			if canvas.CellAt(2, y).Content == "w" && !sameSurface(canvas.CellAt(paneWidth-2, y).Style.Bg, color.RGBA{15, 23, 42, 255}) {
				t.Fatal("wrapped row has a background gap")
			}
		}
	}
}

func TestTranscriptSurfaceThemeChangeAndExplicitBackground(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "plain \x1b[48;2;1;2;3mEXPLICIT\x1b[0m tail"}})
	canvas := surfaceCanvas(ui, 100, 30)
	row := surfaceRow(t, canvas, "EXPLICIT")
	if !sameSurface(canvas.CellAt(8, row).Style.Bg, color.RGBA{1, 2, 3, 255}) {
		t.Fatal("explicit background lost")
	}
	changed := theme.DarkDefault()
	changed.UserBg = "#334455"
	tui.UseTheme(changed)
	canvas = surfaceCanvas(ui, 100, 30)
	if !sameSurface(canvas.CellAt(0, row).Style.Bg, color.RGBA{51, 68, 85, 255}) {
		t.Fatal("cached content retained old theme surface")
	}
	if !sameSurface(canvas.CellAt(8, row).Style.Bg, color.RGBA{1, 2, 3, 255}) {
		t.Fatal("repaint overwrote explicit background")
	}
	tui.UseTheme(theme.Theme{})
	canvas = surfaceCanvas(ui, 100, 30)
	if canvas.CellAt(0, row).Style.Bg != nil {
		t.Fatal("zero theme forced a background")
	}
}

func TestTranscriptGutterSelectsWithoutTogglingOrShifting(t *testing.T) {
	ui := drawerFixture()
	before := ansi.Strip(ui.View().Content)
	row := -1
	for y, line := range strings.Split(before, "\n") {
		if strings.Contains(line, "arguments:") {
			row = y
			break
		}
	}
	if row < 0 {
		t.Fatal("fixture missing arguments")
	}
	ui.Update(tea.MouseClickMsg{X: 0, Y: row, Button: tea.MouseLeft})
	after := ansi.Strip(ui.View().Content)
	if !strings.Contains(after, "INPUT_MARKER") {
		t.Fatal("gutter toggled arguments")
	}
	for _, label := range []string{"arguments:", "INPUT_MARKER"} {
		find := func(s string) int {
			for _, line := range strings.Split(s, "\n") {
				if x := strings.Index(line, label); x >= 0 {
					return x
				}
			}
			return -1
		}
		if find(before) != find(after) {
			t.Fatalf("selection shifted %s", label)
		}
	}
	clickToolLabel(t, ui, "arguments:")
	if strings.Contains(ansi.Strip(ui.View().Content), "INPUT_MARKER") {
		t.Fatal("content click no longer toggles")
	}
}

func TestTranscriptActivityInheritsAssistantSurface(t *testing.T) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	ui.Update(teasink.ConversationStartedMsg{TaskID: "surface", Prompt: "inspect"})
	ui.Update(teasink.ContentMsg{TaskID: "surface", Delta: "ANSWER"})
	ui.Update(teasink.ToolStartedMsg{TaskID: "surface", ToolID: "call", ExecutionID: "call", ToolName: "inspect", Parameters: map[string]any{"path": "BODY"}})
	clickToolLabel(t, ui, "[+] tools")
	clickToolLabel(t, ui, "[+] ◌ inspect")
	canvas := surfaceCanvas(ui, 100, 40)
	for _, label := range []string{"ANSWER", "tools (1)", "arguments:", "BODY"} {
		row := surfaceRow(t, canvas, label)
		if !sameSurface(canvas.CellAt(0, row).Style.Bg, color.RGBA{15, 23, 42, 255}) || !sameSurface(canvas.CellAt(98, row).Style.Bg, color.RGBA{15, 23, 42, 255}) {
			t.Fatalf("%s not on continuous agent surface", label)
		}
	}
}

func TestRailFreeStreamingKeepsCachedParagraphStarts(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	for _, palette := range []theme.Theme{{}, theme.DarkDefault()} {
		tui.UseTheme(palette)
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		ui.Update(teasink.ConversationStartedMsg{TaskID: "stream", Prompt: "inspect"})
		ui.Update(teasink.ContentMsg{TaskID: "stream", Delta: "FIRST paragraph.\n\nSECOND paragraph."})
		_ = ui.View()
		for _, delta := range []string{" tail", "\n\nTHIRD paragraph.", " end"} {
			ui.Update(teasink.ContentMsg{TaskID: "stream", Delta: delta})
			view := ansi.Strip(ui.View().Content)
			for _, text := range []string{"FIRST paragraph.", "SECOND paragraph."} {
				if !strings.Contains(view, "  "+text) {
					t.Fatalf("cached streaming paragraph lost its text/inset: %s", view)
				}
			}
		}
	}
}
