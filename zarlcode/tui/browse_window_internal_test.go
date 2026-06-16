package tui

import (
	"strings"
	"testing"
)

// flatLayoutReference builds the entire browse layout the simple way —
// every item's lines with blank separators between turn-boundary items —
// mirroring the contract layoutIndex encodes. The windowed renderBrowse
// must agree with a slice of this reference at every scroll position.
func flatLayoutReference(tl *timeline, width int) []string {
	var all []string
	for i, it := range tl.items {
		if i > 0 && !itemNested(it) {
			all = append(all, "")
		}
		all = append(all, tl.renderItem(it, width)...)
	}
	return all
}

func TestRenderBrowse_MatchesFlatSliceEveryScroll(t *testing.T) {
	tl := newTimeline()
	// A mix of turn-boundary items (get separators) and nested items
	// (no separator), some multi-line, so the window math exercises
	// separators landing on the top/bottom edge of the viewport.
	tl.addUser("first user message that is long enough to wrap a couple of lines across the width")
	tl.appendThinking("t1", 0, "thinking one\nwith two lines")
	tl.appendContent("t1", 0, "assistant answer paragraph one\n\nparagraph two here")
	tl.addNotice("a notice line")
	tl.addUser("second message")
	tl.appendContent("t2", 0, "another answer that spans\nmultiple\nlines for good measure")
	tl.addUser("third")

	const width, height = 40, 6
	tl.viewWidth, tl.viewHeight = width, height
	tl.browsing = true

	ref := flatLayoutReference(tl, width)
	if len(ref) <= height {
		t.Fatalf("test needs a layout taller than the viewport; got %d lines", len(ref))
	}

	for top := 0; top <= len(ref); top++ {
		tl.scrollTop = top
		view := tl.renderBrowse(width, height)

		// renderBrowse clamps scrollTop; compute the expected window from
		// the clamped value and strip the selection rail before comparing.
		clamped := top
		if maxTop := len(ref) - height; clamped > maxTop {
			clamped = maxTop
		}
		if clamped < 0 {
			clamped = 0
		}
		end := min(clamped+height, len(ref))
		want := ref[clamped:end]

		if len(view) != len(want) {
			t.Fatalf("top=%d: view has %d lines, want %d", top, len(view), len(want))
		}
		for i := range want {
			got := stripRail(view[i])
			if got != want[i] {
				t.Fatalf("top=%d line %d:\n got %q\nwant %q", top, i, got, want[i])
			}
			// visItem/visLocal must point back at a line that actually
			// renders the same text (or a separator for blank lines).
			if tl.visItem[i] == -1 {
				if want[i] != "" {
					t.Fatalf("top=%d line %d marked separator but text is %q", top, i, want[i])
				}
				continue
			}
			it := tl.items[tl.visItem[i]]
			lines := tl.renderItem(it, width)
			if tl.visLocal[i] >= len(lines) || lines[tl.visLocal[i]] != want[i] {
				t.Fatalf("top=%d line %d: visItem/visLocal (%d,%d) does not map to %q",
					top, i, tl.visItem[i], tl.visLocal[i], want[i])
			}
		}
	}
}

// stripRail removes the selection rail prefix renderBrowse prepends to
// the cursor line so the remaining text can be compared to the layout.
func stripRail(s string) string {
	rail := palette.Primary.On("▎")
	return strings.TrimPrefix(s, rail)
}

func BenchmarkRenderBrowse_1000Items(b *testing.B) {
	tl := newTimeline()
	for range 1000 {
		tl.addUser("user message number with a little text to wrap")
		tl.appendContent("turn", 0, "assistant reply paragraph that spans a couple lines\n\nsecond paragraph")
	}
	const width, height = 80, 30
	tl.viewWidth, tl.viewHeight = width, height
	tl.browsing = true
	tl.renderBrowse(width, height) // warm the per-item render cache
	tl.scrollTop = 1200
	b.ResetTimer()
	for range b.N {
		tl.renderBrowse(width, height)
	}
}
