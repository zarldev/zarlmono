package tui

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Transcript content must wrap to the viewport width, including
// unbreakable long tokens (URLs, hashes, paths) that word-wrap can't break
// on spaces. An overflowing line either corrupts the layout or gets clipped
// at draw — either way the user loses content. No rendered line may exceed
// the width it was rendered for.
func TestTimelineRenderNoOverflow(t *testing.T) {
	tl := newTimeline()
	tl.addUser("normal text then a " + strings.Repeat("x", 300) + " unbreakable token")
	tl.startTurn("t1", 0)
	tl.appendContent("t1", 0, "assistant reply with "+strings.Repeat("y", 250)+" inline")
	tl.endTurn("t1")

	for _, w := range []int{40, 80, 120} {
		for i, line := range tl.renderViewport(w, 200) {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width=%d: line %d overflows (%d cols > %d):\n%q", w, i, got, w, ansi.Strip(line))
			}
		}
	}
}

func TestConversationItemsRenderWithOwnershipAnchors(t *testing.T) {
	user := ansi.Strip(strings.Join((&userItem{text: "hello\ncontinued"}).render(30), "\n"))
	for _, want := range []string{"[you]\n│ hello", "│ continued"} {
		if !strings.Contains(user, want) {
			t.Fatalf("user render missing %q: %q", want, user)
		}
	}
	assistant := ansi.Strip(strings.Join((&assistantItem{content: "reply\ncontinued", done: true}).render(30), "\n"))
	for _, want := range []string{"[zarl]\n│", "reply", "│", "continued"} {
		if !strings.Contains(assistant, want) {
			t.Fatalf("assistant render missing %q: %q", want, assistant)
		}
	}
}

func TestDrawTimelineKeepsOnlyTopChrome(t *testing.T) {
	m := New()
	m.timeline.addUser("assess the transcript surface")
	buf := uv.NewScreenBuffer(60, 8)

	m.drawTimeline(buf, buf.Bounds())

	lines := strings.Split(ansi.Strip(buf.Render()), "\n")
	if len(lines) < 8 {
		t.Fatalf("rendered %d rows, want at least 8", len(lines))
	}
	if !strings.Contains(lines[0], "ƶ") || !strings.Contains(lines[0], "idle") {
		t.Fatalf("top status bar missing orientation or run state: %q", lines[0])
	}
	if !strings.Contains(lines[1], "─") {
		t.Fatalf("top status bar missing divider: %q", lines[1])
	}
	if !strings.Contains(lines[0], "follow") {
		t.Fatalf("top status bar missing viewport state: %q", lines[0])
	}
	ownerAnchored := false
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "[you]") {
			ownerAnchored = true
			break
		}
	}
	if !ownerAnchored {
		t.Fatalf("transcript ownership anchor should start at pane edge:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[len(lines)-1], "└") || strings.Contains(lines[len(lines)-1], "┘") {
		t.Fatalf("timeline retained a bottom border: %q", lines[len(lines)-1])
	}
	for i, line := range lines[1:] {
		if strings.HasSuffix(line, "│") {
			t.Fatalf("timeline retained a side border on row %d: %q", i+1, line)
		}
	}
}

func TestTimelineViewportStateLabel(t *testing.T) {
	tl := newTimeline()
	if got := tl.viewportStateLabel(); got != "follow 100%" {
		t.Fatalf("default state = %q, want follow position", got)
	}
	tl.viewHeight = 2
	for i := range 8 {
		tl.addUser("message " + itoa(i))
	}
	tl.enterBrowse()
	if got := tl.viewportStateLabel(); !strings.HasPrefix(got, "browse ") || !strings.HasSuffix(got, "%") || got == "browse 0%" {
		t.Fatalf("browse state = %q, want non-zero position", got)
	}
	tl.scrollTop = 0
	if got := tl.viewportStateLabel(); got != "browse 0%" {
		t.Fatalf("browse top state = %q, want top position", got)
	}
	g := tl.scrollbarGeom(tl.viewHeight)
	if !g.Active || g.ThumbStart != 0 {
		t.Fatalf("browse top scrollbar = %+v, want active thumb at top", g)
	}
	tl.exitBrowse()
	g = tl.scrollbarGeom(tl.viewHeight)
	if !g.Active || g.ThumbEnd != g.Height-1 {
		t.Fatalf("follow scrollbar = %+v, want active thumb at bottom", g)
	}
	tl.startSelection()
	if got := tl.viewportStateLabel(); got != "visual" {
		t.Fatalf("selection state = %q, want visual", got)
	}
}

func TestNestedActivityBindsToAssistantRail(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t1", 0)
	tl.appendContent("t1", 0, "working")
	tl.startToolWithParent("t1", 0, "c1", "read", "main.go", "", 0)

	out := ansi.Strip(strings.Join(tl.renderViewport(60, 20), "\n"))
	if !strings.Contains(out, "[zarl]\n│") || !strings.Contains(out, "working") {
		t.Fatalf("assistant owner anchor missing:\n%s", out)
	}
	if !strings.Contains(out, "│ [+] tools (1)") {
		t.Fatalf("tool activity is not bound to assistant rail:\n%s", out)
	}
}

func TestTranscriptContentGeometry(t *testing.T) {
	for _, tc := range []struct {
		available int
		width     int
	}{
		{available: 60, width: 60},
		{available: maxTranscriptWidth, width: maxTranscriptWidth},
		{available: 150, width: maxTranscriptWidth},
	} {
		width := transcriptContentWidth(tc.available)
		if width != tc.width {
			t.Errorf("width(%d) = %d, want %d", tc.available, width, tc.width)
		}
	}
}
