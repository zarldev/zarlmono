package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// When the turn list is longer than the nav rail, the selected entry must
// still render — the nav rail windows around the cursor instead of drawing
// from index 0 and clipping the selection off the bottom.
func TestDiffBrowser_SelectedEntryVisibleWhenScrolled(t *testing.T) {
	ws := NewWorkingSet("/repo")
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("turn-%d", i)
		ws.StartTurn(id)
		ws.RecordDiff(fmt.Sprintf("file%02d.go", i), "@@\n+x")
		ws.CompleteTurn(id)
	}
	d := newDiffBrowser(ws)
	entries := d.entries()
	d.cursor = len(entries) - 1 // select the last turn
	last := entries[d.cursor].label

	out := drawDiffBrowserWithSize(t, d, 150, 16) // short → nav rail overflows

	if !strings.Contains(out, last) {
		t.Errorf("selected entry %q must stay visible when the list overflows:\n%s", last, out)
	}
	if strings.Contains(out, "turn #1 ") {
		t.Errorf("the top of an overflowing list should scroll off:\n%s", out)
	}
}

func TestDiffBrowser_GroupsByTurnInMutationOrder(t *testing.T) {
	ws := seededDiffBrowserWorkingSet()
	d := newDiffBrowser(ws)
	out := drawDiffBrowser(t, d)

	for _, want := range []string{"diff browser · by turn", "turn #1", "turn #2", "2 files · 2 diffs", "a.go · mutation #1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("turn diff browser missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "turn #1") > strings.Index(out, "turn #2") {
		t.Fatalf("turns rendered out of order:\n%s", out)
	}
}

func TestDiffBrowser_TabSwitchesToFileAndSessionPatch(t *testing.T) {
	ws := seededDiffBrowserWorkingSet()
	d := newDiffBrowser(ws)
	d.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	out := drawDiffBrowser(t, d)
	for _, want := range []string{"diff browser · by file", "a.go", "b.go", "2 diffs · +2 -1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("file diff browser missing %q:\n%s", want, out)
		}
	}

	d.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	out = drawDiffBrowser(t, d)
	for _, want := range []string{"session patch", "2 files · 3 diffs", "a.go · mutation #3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session diff browser missing %q:\n%s", want, out)
		}
	}
}

func TestDiffBrowser_ScrollsPreview(t *testing.T) {
	ws := NewWorkingSet("/repo")
	ws.StartTurn("turn-1")
	var b strings.Builder
	b.WriteString("@@\n")
	for range 40 {
		b.WriteString("+line\n")
	}
	ws.RecordDiff("long.go", b.String())
	d := newDiffBrowser(ws)
	out := drawDiffBrowserWithSize(t, d, 100, 16)
	if !strings.Contains(out, "+line") {
		t.Fatalf("initial diff preview missing line:\n%s", out)
	}
	d.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if d.scroll == 0 {
		t.Fatal("pgdown should advance diff preview scroll")
	}
}

func seededDiffBrowserWorkingSet() *WorkingSet {
	ws := NewWorkingSet("/repo")
	ws.StartTurn("turn-1")
	ws.RecordDiff("a.go", "@@\n+a1")
	ws.RecordDiff("b.go", "@@\n+b1")
	ws.CompleteTurn("turn-1")
	ws.StartTurn("turn-2")
	ws.RecordDiff("a.go", "@@\n-a1\n+a2")
	return ws
}

func drawDiffBrowser(t *testing.T, d *diffBrowser) string {
	t.Helper()
	return drawDiffBrowserWithSize(t, d, 150, 36)
}

func drawDiffBrowserWithSize(t *testing.T, d *diffBrowser, w, h int) string {
	t.Helper()
	scr := uv.NewScreenBuffer(w, h)
	d.draw(scr, uv.Rect(0, 0, w, h))
	return ansi.Strip(scr.Render())
}
