package tui

import uv "github.com/charmbracelet/ultraviolet"

// uiLayout holds the computed pane rectangles. Recomputed on every
// resize; this is the single place where ultrawide multi-pane vs narrow
// single-column is decided. We target the home ultrawide form factor
// (~220-280 cols) first and degrade gracefully below sidebarMinWidth.
type uiLayout struct {
	header  uv.Rectangle
	main    uv.Rectangle // the run timeline
	sidebar uv.Rectangle // state sidebar; empty (collapsed) below sidebarMinWidth
	editor  uv.Rectangle
	status  uv.Rectangle
}

const (
	headerHeight    = 0   // app/mode/model are folded into the timeline title
	editorMinHeight = 3   // box: border + one input row
	editorMaxHeight = 8   // box: border + six input rows, matching intro prompt
	statusHeight    = 1   // single-line hint bar
	sidebarWidth    = 56  // fits the state summary sections comfortably
	sidebarMinWidth = 160 // below this total width the sidebar collapses
)

// computeLayout slices a w×h screen into pane rects. Header pins to the
// top, editor + status to the bottom, and the middle is the timeline —
// flanked by the sidebar at ultrawide widths, full-width below
// sidebarMinWidth. Negative/zero middle heights on tiny terminals yield
// empty rects, which Draw skips and ultraviolet clips.
func computeLayout(w, h int) uiLayout {
	return computeLayoutWithEditorLines(w, h, 1)
}

func computeLayoutWithEditorLines(w, h int, editorLines int) uiLayout {
	if w <= 0 || h <= 0 {
		return uiLayout{}
	}
	var l uiLayout
	l.header = uv.Rect(0, 0, w, headerHeight)
	editorHeight := max(editorLines+2, editorMinHeight)
	if editorHeight > editorMaxHeight {
		editorHeight = editorMaxHeight
	}

	bottom := editorHeight + statusHeight
	midTop := headerHeight
	midH := max(h-midTop-bottom, 0)

	if w >= sidebarMinWidth && midH > 0 {
		l.main = uv.Rect(0, midTop, w-sidebarWidth, midH)
		l.sidebar = uv.Rect(w-sidebarWidth, midTop, sidebarWidth, midH)
	} else {
		l.main = uv.Rect(0, midTop, w, midH)
		// sidebar stays the zero rect (collapsed)
	}

	l.editor = uv.Rect(0, h-bottom, w, editorHeight)
	l.status = uv.Rect(0, h-statusHeight, w, statusHeight)
	return l
}
