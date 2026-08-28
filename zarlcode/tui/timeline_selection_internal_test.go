package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTimelineSelection_CopyCleansRailsAndANSI(t *testing.T) {
	tl := newTimeline()
	tl.addUser("hello\n    code")
	tl.appendContent("t1", 0, "answer")
	tl.renderViewport(80, 10)
	tl.cursorTop()
	if !tl.startSelection() {
		t.Fatal("startSelection returned false")
	}
	tl.moveSelectionBottom()

	got := tl.selectedText()
	if strings.Contains(got, "\x1b[") || got != ansi.Strip(got) {
		t.Fatalf("selected text contains ANSI: %q", got)
	}
	if strings.Contains(got, "▌") || strings.Contains(got, "▎") {
		t.Fatalf("selected text contains transcript rails: %q", got)
	}
	for _, want := range []string{"hello", "answer", "    code"} {
		if !strings.Contains(got, want) {
			t.Fatalf("selected text missing %q:\n%q", want, got)
		}
	}
}

func TestTimelineSelection_BackwardRangeAndCancel(t *testing.T) {
	tl := newTimeline()
	tl.addUser("first")
	tl.addUser("second")
	tl.addUser("third")
	tl.renderViewport(80, 8)

	tl.enterBrowse()
	if !tl.browsing {
		t.Fatal("enterBrowse did not enable browsing")
	}
	for tl.sel < len(tl.items)-1 {
		tl.cursorDown()
	}
	if !tl.startSelection() {
		t.Fatal("startSelection returned false")
	}
	tl.selection.anchor = tl.totalLines(tl.lwidth()) - 1 // select through the turn body, not only its owner row
	tl.selection.head = 0

	got := tl.selectedText()
	if !strings.Contains(got, "first") || !strings.Contains(got, "third") {
		t.Fatalf("backward selection did not normalize range: %q", got)
	}
	tl.cancelSelection()
	if tl.selectionActive() {
		t.Fatal("selection still active after cancel")
	}
	if !tl.browsing {
		t.Fatal("cancelSelection should keep browse mode active")
	}
}

func TestTimelineSelection_ClampAndKeepHeadVisible(t *testing.T) {
	tl := newTimeline()
	for i := range 12 {
		tl.addUser("line " + string(rune('a'+i)))
	}
	tl.renderViewport(80, 4)
	tl.cursorTop()
	if !tl.startSelection() {
		t.Fatal("startSelection returned false")
	}
	tl.moveSelection(-100)
	if tl.selection.head != 0 {
		t.Fatalf("head after large negative move = %d, want 0", tl.selection.head)
	}
	tl.moveSelection(1000)
	total := tl.totalLines(tl.lwidth())
	if tl.selection.head != total-1 {
		t.Fatalf("head after large positive move = %d, want %d", tl.selection.head, total-1)
	}
	if tl.selection.head < tl.scrollTop || tl.selection.head >= tl.scrollTop+tl.viewHeight {
		t.Fatalf("selection head not visible: head=%d scrollTop=%d height=%d", tl.selection.head, tl.scrollTop, tl.viewHeight)
	}
}

func TestTimelineSelection_RenderHighlightsAndSuppressesBrowseRail(t *testing.T) {
	tl := newTimeline()
	tl.addUser("one")
	tl.addUser("two")
	tl.renderViewport(80, 5)
	tl.cursorTop()
	if !tl.startSelection() {
		t.Fatal("startSelection returned false")
	}
	tl.moveSelection(2)

	view := strings.Join(tl.renderViewport(80, 5), "\n")
	if strings.Contains(ansi.Strip(view), "▎") {
		t.Fatalf("browse rail should be suppressed while selecting:\n%s", ansi.Strip(view))
	}
}
