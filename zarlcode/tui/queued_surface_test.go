package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestQueuedMessageSurfaceChangesOnlyOnInjection(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	for _, name := range []string{"midnight", "parchment", "dracula"} {
		palette, _ := theme.ByName(name)
		tui.UseTheme(palette)
		ui := tui.New()
		ui.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		live := newQueueLive(t)
		ui.SetLiveRunner(live)
		ui.Update(teasink.ConversationStartedMsg{TaskID: "queue", Prompt: "NORMAL_USER"})
		ui.Submit("QUEUED_FIRST\n  preserved indentation")
		ui.Submit("QUEUED_SECOND")
		canvas := surfaceCanvas(ui, 100, 40)
		normal := canvas.CellAt(0, surfaceRow(t, canvas, "NORMAL_USER")).Style.Bg
		first := surfaceRow(t, canvas, "QUEUED_FIRST")
		queued := canvas.CellAt(0, first).Style.Bg
		if queued == nil || sameSurface(queued, normal) {
			t.Fatalf("%s queue surface indistinguishable", name)
		}
		if sameSurface(colorprofile.ANSI256.Convert(queued), colorprofile.ANSI256.Convert(normal)) {
			t.Fatalf("%s queue surface collapses in ANSI256", name)
		}
		for y := first - 1; y <= first+2; y++ {
			for x := range 99 {
				if !sameSurface(canvas.CellAt(x, y).Style.Bg, queued) {
					t.Fatalf("%s queued surface has gap at %d,%d", name, x, y)
				}
			}
		}
		// Completion of unrelated work must not mark queued input as sent.
		ui.Update(teasink.ContentMsg{TaskID: "queue", Delta: "working"})
		canvas = surfaceCanvas(ui, 100, 40)
		if !sameSurface(canvas.CellAt(0, surfaceRow(t, canvas, "QUEUED_FIRST")).Style.Bg, queued) {
			t.Fatal("streaming changed queue state colour")
		}
		message, ok := live.PopQueuedInput()
		if !ok {
			t.Fatal("fixture lost queued input")
		}
		ui.Update(teasink.SteerInjectedMsg{TaskID: "queue", Messages: []llm.Message{message}})
		for _, candidate := range []*tui.UI{ui, restoredQueuedSurface(ui)} {
			canvas = surfaceCanvas(candidate, 100, 40)
			if !sameSurface(canvas.CellAt(0, surfaceRow(t, canvas, "QUEUED_FIRST")).Style.Bg, normal) {
				t.Fatal("injected message retained queued background")
			}
			if !sameSurface(canvas.CellAt(0, surfaceRow(t, canvas, "QUEUED_SECOND")).Style.Bg, queued) {
				t.Fatal("injecting first message recoloured later queued input")
			}
			if got := strings.Count(ansi.Strip(candidate.View().Content), "◷ queued"); got != 1 {
				t.Fatalf("queued label count=%d, want 1", got)
			}
		}
	}
}

func restoredQueuedSurface(source *tui.UI) *tui.UI {
	restored := tui.New()
	restored.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	restored.RestoreCanonicalTranscript(source.CanonicalThread())
	return restored
}

func TestQueuedMessageSurfaceRecoloursAndPreservesSelection(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	ui.SetLiveRunner(newQueueLive(t))
	ui.Update(teasink.ConversationStartedMsg{TaskID: "queue", Prompt: "NORMAL"})
	ui.Submit("QUEUED")
	for _, palette := range []theme.Theme{theme.DarkDefault(), {Bg: "#eeeeee", Fg: "#111111", Secondary: "#0066cc", Highlight: "#aabbcc"}, {}} {
		tui.UseTheme(palette)
		canvas := surfaceCanvas(ui, 100, 40)
		normal := canvas.CellAt(0, surfaceRow(t, canvas, "NORMAL")).Style.Bg
		queued := canvas.CellAt(0, surfaceRow(t, canvas, "QUEUED")).Style.Bg
		if palette.Bg == "" {
			if queued != nil {
				t.Fatal("zero theme forced queued colour")
			}
		} else if sameSurface(normal, queued) {
			t.Fatal("theme switch retained stale queue surface")
		}
	}
	tui.UseTheme(theme.DarkDefault())
	canvas := surfaceCanvas(ui, 100, 40)
	row := surfaceRow(t, canvas, "QUEUED")
	queued := canvas.CellAt(0, row).Style.Bg
	ui.Update(tea.MouseClickMsg{X: 0, Y: row, Button: tea.MouseLeft})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	ui.Update(tea.KeyPressMsg{Code: 'v'})
	canvas = surfaceCanvas(ui, 100, 40)
	if sameSurface(canvas.CellAt(0, row).Style.Bg, queued) {
		t.Fatal("queued background overrode visual selection")
	}
}
