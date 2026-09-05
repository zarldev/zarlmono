package tui

import (
	"strings"

	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// --- cached, viewport-bounded render ---

// renderItem serves an item's lines from cache when its (width, version,
// theme) is unchanged. Finished items keep a stable version, so they render
// once per width and are then frozen — until a theme switch bumps themeGen,
// which invalidates the baked-in colours and forces a recolour.
func (tl *timeline) renderItem(it item, width int) []string {
	revision := it.version()
	if e, ok := tl.cache[it]; ok && e.width == width && e.ver == revision && e.gen == themeGen {
		return e.lines
	}
	lines := it.render(width)
	tl.cache[it] = cacheEntry{width: width, ver: revision, gen: themeGen, lines: lines}
	return lines
}

// renderViewport renders only enough items from the bottom to fill
// height lines, then returns the last height lines (auto-follow). Items
// scrolled off the top are never rendered — cost is O(viewport), not
// O(history).
func (tl *timeline) renderViewport(width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	// Cache the geometry so line-based navigation + the scrollbar can
	// clamp/scroll against wrap-accurate totals without re-measuring.
	tl.viewWidth, tl.viewHeight = width, height
	if tl.browsing {
		return tl.renderBrowse(width, height)
	}
	return tl.renderTail(width, height)
}

// renderTail renders the bottom of the timeline (auto-follow), bounded to
// the viewport — the streaming-fast path.
func (tl *timeline) renderTail(width, height int) []string {
	vis := tl.tailBlocks[:0] // newest-first
	total := 0
	for i := len(tl.items) - 1; i >= 0 && total < height; i-- {
		ls := tl.renderItem(tl.items[i], width)
		if len(ls) == 0 {
			// Invisible placeholders (notably a turn's unloaded skills item)
			// occupy no viewport space and must not stop the tail scan early.
			continue
		}
		// In forward render order, a separator belongs immediately before the
		// newer block. vis is newest-first here, so its current last entry is
		// the block that will follow this one.
		if len(vis) > 0 && !itemNested(vis[len(vis)-1].it) {
			total++
		}
		vis = append(vis, timelineRenderBlock{i, tl.items[i], ls})
		total += len(ls)
	}
	var out []string
	vItem, vLocal := tl.visItem[:0], tl.visLocal[:0]
	for j := len(vis) - 1; j >= 0; j-- {
		if len(out) > 0 && !itemNested(vis[j].it) {
			out = append(out, "") // blank only before turn-boundary items
			vItem = append(vItem, -1)
			vLocal = append(vLocal, 0)
		}
		for k, ln := range vis[j].lines {
			out = append(out, ln)
			vItem = append(vItem, vis[j].idx)
			vLocal = append(vLocal, k)
		}
	}
	if len(out) > height {
		cut := len(out) - height
		out, vItem, vLocal = out[cut:], vItem[cut:], vLocal[cut:]
	}
	tl.tailBlocks = vis
	tl.visItem, tl.visLocal = vItem, vLocal
	return out
}

var nestPad = palette.Assistant.On("│ ")

func turnOwnerPrefixes(label string, style func(string) string) (string, string) {
	anchor := bracketed(style(label))
	rail := style("│ ")
	return anchor, rail
}

// itemNested reports whether an item is turn activity (rendered tight,
// grouped under a response) rather than a turn-boundary item that gets a
// blank line before it.
func itemNested(it item) bool {
	switch v := it.(type) {
	case *thinkingItem:
		return v.nested
	case *groupItem:
		return v.nested
	case *planItem:
		return v.nested
	case *subAgentItem:
		return v.nested
	case *skillsItem:
		return v.nested
	}
	return false
}

// scrollbarWidth is the column count reserved for the right-edge
// scrollbar gutter — always 1, kept named so future refactors can
// raise it without hunting for the magic number.
const scrollbarWidth = 1

// drawTimeline paints the timeline beneath a persistent status bar. The main
// transcript stays open on the left, right, and bottom so its content reads as
// the primary surface rather than another boxed pane.
func (m *UI) drawTimeline(scr uv.Screen, r uv.Rectangle) {
	m.drawTimelineTopBar(scr, r)
	w, h := r.Dx(), r.Dy()
	if w < 4 || h < 4 {
		return
	}
	// Reserve only the final column for the scrollbar. The transcript itself
	// starts at the pane edge; ownership anchors provide its visual gutter.
	innerW, innerH := w-scrollbarWidth, h-2
	if innerW < 1 {
		innerW = 1
	}
	// With mode 2027 the terminal + renderer agree on grapheme width, so
	// emoji render aligned. Without it, emoji widths are unpredictable and
	// bleed across panes — strip them so the cell grid stays sound.
	keepEmoji := m.widthMethod == ansi.GraphemeWidth
	contentW := transcriptContentWidth(innerW)
	lines := m.timeline.renderViewport(contentW, innerH)
	for i, ln := range lines {
		if !keepEmoji {
			ln = stripWide(ln)
		}
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+2+i, contentW, 1), ln)
	}
	// Paint the scrollbar gutter on the right edge.
	m.drawTimelineScrollbar(scr, r, innerH, len(lines))
}

const maxTranscriptWidth = 110

func transcriptContentWidth(available int) int {
	if available < 1 {
		return 1
	}
	return min(available, maxTranscriptWidth)
}

func (m *UI) drawTimelineTopBar(scr uv.Screen, r uv.Rectangle) {
	if r.Dx() < 1 || r.Dy() < 1 {
		return
	}
	left, model, viewport := m.transcriptHeaderSegments()
	right := viewport
	const separatorWidth = 3
	modelWidth := r.Dx() - ansi.StringWidth(left) - 1 - ansi.StringWidth(viewport) - separatorWidth
	if model != "" && modelWidth > 0 {
		right = ansi.Truncate(model, modelWidth, "…") + palette.Subtle.On("  ·  ") + viewport
	}
	line := rowLayout(left, right, r.Dx())
	drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), line)
	drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+1, r.Dx(), 1), palette.Border.On(strings.Repeat("─", r.Dx())))
}

func (m *UI) transcriptHeaderSegments() (string, string, string) {
	// The transcript needs a quiet orientation mark, not a second title. The
	// composer and terminal already establish product context, so retain identity
	// as the compact ƶ mark and let operational state carry the row.
	left := palette.Primary.On("ƶ")

	run := "○ idle"
	runTone := palette.Muted
	if m.session.Run.Running {
		run = runActivityGlyph(m.frame, true) + " running"
		runTone = palette.Success
		if tps := m.session.Run.liveTokPerSec(); tps > 0 {
			run += "  ·  " + itoa(int(tps+0.5)) + " tok/s"
		}
	}
	left += palette.Subtle.On("  ·  ") + runTone.On(run)
	model := ""
	if name := strings.ToLower(m.session.Model); name != "" {
		model = palette.Muted.On(name)
	}
	viewport := palette.Info.On(m.timeline.viewportStateLabel())
	return left, model, viewport
}

func (tl *timeline) viewportStateLabel() string {
	if tl.selectionActive() {
		return "visual"
	}
	_, _, total := tl.layoutIndex(tl.lwidth())
	maxScroll := maxOffset(total, tl.viewHeight)
	if !tl.browsing {
		return "follow 100%"
	}
	percent := 100
	if maxScroll > 0 {
		percent = int(float64(tl.scrollTop) / float64(maxScroll) * 100)
	}
	return "browse " + itoa(percent) + "%"
}

// drawTimelineScrollbar paints a 1-col scrollbar gutter at the right edge
// of the open timeline pane. It stays hidden when there is no scroll range, so
// the gutter does not read as a replacement right border. Track is Border-
// coloured and the thumb is Primary in both follow and browse modes.
func (m *UI) drawTimelineScrollbar(scr uv.Screen, r uv.Rectangle, height int, _ int) {
	if height <= 0 {
		return
	}
	g := m.timeline.scrollbarGeom(height)
	if !g.Active {
		return
	}
	x := r.Max.X - 1 // open transcript: scrollbar owns the final column

	trackGlyph := palette.Border.On("│")
	thumbGlyph := palette.Primary.On("█")
	for i := range height {
		glyph := trackGlyph
		if i >= g.ThumbStart && i <= g.ThumbEnd {
			glyph = thumbGlyph
		}
		drawLine(scr, uv.Rect(x, r.Min.Y+2+i, 1, 1), glyph)
	}
}

// scrollbarGeom describes the live scrollbar geometry derived from the
// timeline's total rendered lines and current viewport position.
type scrollbarGeom struct {
	Active     bool
	Height     int
	ThumbStart int // inclusive, [0, Height)
	ThumbEnd   int // inclusive
}

// scrollbarGeom returns the scrollbar geometry for the given gutter height.
// When browsing we know the total lines and offset from renderBrowse;
// when auto-following (tail mode) the thumb sits at the bottom.
func (tl *timeline) scrollbarGeom(height int) scrollbarGeom {
	if height <= 0 {
		return scrollbarGeom{}
	}
	// The owned layout index is incrementally cached, so follow mode can expose
	// the real bottom-position thumb without flattening or re-rendering history.
	_, _, total := tl.layoutIndex(tl.lwidth())
	offset := tl.scrollTop
	if !tl.browsing {
		offset = maxOffset(total, height)
	}
	return paneScrollbarGeom(total, height, offset)
}

// --- text helpers ---

// wrapText word-wraps s to width using lipgloss, which is grapheme-aware.
// Preserves explicit newlines by rendering each paragraph separately.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	style := lg.NewStyle().Width(width)
	var out []string
	for para := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		rendered := style.Render(para)
		// lipgloss trims trailing newlines; split back into lines.
		out = append(out, strings.Split(rendered, "\n")...)
	}
	return out
}

// indentLines prefixes a vertical rail per nesting level, so depth>0
// (sub-agent) output reads as a framed, nested block.
func indentLines(lines []string, depth int) []string {
	if depth <= 0 {
		return lines
	}
	return prefixLines(lines, strings.Repeat("⇢ ", depth))
}

// maxContentWidth caps the measure for prose/markdown so content stays
// readable on wide terminals instead of stretching across the full pane
// (a 4-column table at ~195 cols is unreadable padding). Panes use the
// screen width; prose does not.
const maxContentWidth = 90

// toolResultMaxLines caps how much of a tool's output the expanded row
// shows (the full result is still in the model's context).
const toolResultMaxLines = 40

func contentWidth(w int) int {
	if w > maxContentWidth {
		return maxContentWidth
	}
	if w < 1 {
		return 1
	}
	return w
}

// stripWide drops emoji, variation selectors, and ZWJ from display text.
// It's the fallback for terminals without mode 2027 (Unicode core): there,
// renderer and terminal disagree on emoji width — a single emoji can occupy
// 1 or 2 cells depending on the font — which desyncs the cell grid and
// bleeds one row into the neighbouring pane (a missing right border). When
// 2027 is negotiated, drawTimeline keeps emoji instead (widths agree).
// BMP line-art/dingbat glyphs (▌ │ ✓ ✗ • °) are width-1 in both tables and
// survive; only astral emoji + selectors go.
func stripWide(s string) string {
	hasWide := false
	for _, r := range s {
		if isWideGrapheme(r) {
			hasWide = true
			break
		}
	}
	if !hasWide {
		return s // common case: nothing to strip, no allocation
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !isWideGrapheme(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isWideGrapheme(r rune) bool {
	switch {
	case r == 0x200D, // zero-width joiner
		r >= 0xFE00 && r <= 0xFE0F,   // variation selectors (incl. VS16)
		r >= 0x1F000 && r <= 0x1FAFF: // astral emoji / pictographs / symbols
		return true
	}
	return false
}

func prefixLines(lines []string, prefix string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = prefix + l
	}
	return out
}
