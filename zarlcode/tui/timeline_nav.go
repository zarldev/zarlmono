package tui

// collapsible is an item whose render state can be toggled (e.g. an
// assistant item's thinking block, or a tool/edit group). toggleSelected
// calls toggle() on the selected item.
type collapsible interface {
	item
	toggle()
}

// toggler is anything whose collapsed state can be flipped — a top-level
// collapsible item, or a sub-entry (a tool / diff row) nested in a group.
type toggler interface{ toggle() }

// togglerFunc adapts a closure to toggler, so a group can hand back a toggler
// that flips a child AND bumps the group's version (the group renders its
// children inline, so it must re-render for the child's new state to show).
type togglerFunc func()

func (f togglerFunc) toggle() { f() }

// hitToggler maps a local line (0-based within an item's rendered block at
// width) to the toggler whose [+]/[-] header sits on that line, or nil. It's
// what turns a mouse click on a transcript row into a collapse/expand — top
// level for groups/thinking, and per sub-entry for the tool/diff rows a group
// renders inline.
type hitToggler interface {
	togglerAt(width, ln int) toggler
}

// browseLineStep is how many lines a wheel notch / line-scroll moves.
const browseLineStep = 3

// Browse navigation is a hybrid:
//
//   - Up/Down move the SELECTION by item (so every item — including the
//     one-line collapsed "thinking" / "tools (N)" blocks — can be landed on
//     and expanded), scrolling only enough to keep it visible (no jump).
//   - PgUp/PgDn and the wheel scroll by LINES/pages so a single entry taller
//     than the pane can be read all the way through; the selection is kept
//     within the viewport as the view moves.
//
// scrollTop is the viewport's top line; sel is the selected item index.

func (tl *timeline) lwidth() int {
	if tl.viewWidth > 0 {
		return tl.viewWidth
	}
	return 80
}

func (tl *timeline) selectableLocals(i, width int) []int {
	if i < 0 || i >= len(tl.items) {
		return nil
	}
	g, ok := tl.items[i].(*groupItem)
	if !ok {
		return []int{0}
	}
	if !g.expanded {
		return []int{0} // just the group header
	}
	// Same child offsets render and togglerAt use, so selection lands exactly
	// on the toggle targets.
	return append([]int{0}, renderChildBlock(g.children, g.childWidth(width)).offsets...)
}

func nextLocal(locs []int, cur int) (int, bool) {
	for _, loc := range locs {
		if loc > cur {
			return loc, true
		}
	}
	return 0, false
}

func prevLocal(locs []int, cur int) (int, bool) {
	for i := len(locs) - 1; i >= 0; i-- {
		if locs[i] < cur {
			return locs[i], true
		}
	}
	return 0, false
}

func (tl *timeline) clampSelLocal(width int) {
	locs := tl.selectableLocals(tl.sel, width)
	if len(locs) == 0 {
		tl.selLocal = 0
		return
	}
	best := locs[0]
	bestDist := absInt(tl.selLocal - best)
	for _, loc := range locs[1:] {
		if dist := absInt(tl.selLocal - loc); dist < bestDist {
			best, bestDist = loc, dist
		}
	}
	tl.selLocal = best
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// layoutIndex returns each item's [start,end) line range in the flat
// browse layout and the total line count, with blank separators between
// turn-boundary items counted exactly as renderBrowse/renderTail emit
// them. Heights come from the per-item render cache (memoised per
// width/version), so this is an O(n) walk of int lengths — no line
// copying, no full-history slice. Navigation and the scrollbar use it to
// clamp and window without flattening the whole transcript every frame.
func (tl *timeline) layoutIndex(width int) ([]int, []int, int) {
	starts := make([]int, len(tl.items))
	ends := make([]int, len(tl.items))
	n := 0
	for i, it := range tl.items {
		if i > 0 && !itemNested(it) {
			n++ // blank separator
		}
		starts[i] = n
		n += len(tl.renderItem(it, width))
		ends[i] = n
	}
	return starts, ends, n
}

func (tl *timeline) pageStep() int {
	if tl.viewHeight > 1 {
		return tl.viewHeight - 1
	}
	return browseLineStep
}

func maxOffset(total, height int) int {
	if m := total - height; m > 0 {
		return m
	}
	return 0
}

func (tl *timeline) clampScroll(total int) {
	if m := maxOffset(total, tl.viewHeight); tl.scrollTop > m {
		tl.scrollTop = m
	}
	if tl.scrollTop < 0 {
		tl.scrollTop = 0
	}
}

func (tl *timeline) enterBrowse() {
	if tl.browsing {
		return
	}
	tl.browsing = true
	tl.sel = len(tl.items) - 1 // freeze on the tail
	tl.selLocal = 0
	tl.scrollToSel()
}

func (tl *timeline) exitBrowse() { tl.browsing = false }

// scrollToSel nudges scrollTop only enough to keep the selected item visible
// (its start, if it's taller than the pane). No-jump: an already-visible
// selection doesn't move the viewport.
func (tl *timeline) scrollToSel() {
	starts, _, total := tl.layoutIndex(tl.lwidth())
	if total == 0 {
		return
	}
	if tl.sel < 0 {
		tl.sel = 0
	}
	if tl.sel > len(tl.items)-1 {
		tl.sel = len(tl.items) - 1
	}
	h := tl.viewHeight
	tl.clampSelLocal(tl.lwidth())
	cTop := starts[tl.sel] + tl.selLocal
	cBot := cTop + 1
	switch {
	case cTop < tl.scrollTop:
		tl.scrollTop = cTop // above the view → scroll up to it
	case cBot > tl.scrollTop+h:
		tl.scrollTop = cBot - h // below the view → scroll down to it
	}
	tl.clampScroll(total)
}

// clampSelToView keeps the selection a visible item after a line/page scroll;
// if the current selection scrolled out, it picks the item at the viewport top.
func (tl *timeline) clampSelToView(starts, ends []int) {
	h := tl.viewHeight
	if h <= 0 || len(tl.items) == 0 {
		return
	}
	top, bot := tl.scrollTop, tl.scrollTop+h
	if tl.sel >= 0 && tl.sel < len(tl.items) && ends[tl.sel] > top && starts[tl.sel] < bot {
		locs := tl.selectableLocals(tl.sel, tl.lwidth())
		line := starts[tl.sel] + tl.selLocal
		if line >= top && line < bot {
			return
		}
		for _, loc := range locs {
			if ln := starts[tl.sel] + loc; ln >= top && ln < bot {
				tl.selLocal = loc
				return
			}
		}
		tl.selLocal = 0
		tl.clampSelLocal(tl.lwidth())
		return
	}
	for i := range tl.items {
		if (starts[i] <= top && top < ends[i]) || (starts[i] >= top && starts[i] < bot) {
			tl.sel = i
			tl.selLocal = 0
			for _, loc := range tl.selectableLocals(i, tl.lwidth()) {
				if ln := starts[i] + loc; ln >= top && ln < bot {
					tl.selLocal = loc
					break
				}
			}
			return
		}
	}
}

// --- selection movement (Up/Down) ---

func (tl *timeline) cursorUp() {
	tl.enterBrowse()
	if loc, ok := prevLocal(tl.selectableLocals(tl.sel, tl.lwidth()), tl.selLocal); ok {
		tl.selLocal = loc
		tl.scrollToSel()
		return
	}
	if tl.sel > 0 {
		tl.sel--
		locs := tl.selectableLocals(tl.sel, tl.lwidth())
		tl.selLocal = locs[len(locs)-1]
	}
	tl.scrollToSel()
}

func (tl *timeline) cursorDown() {
	if !tl.browsing {
		return
	}
	if loc, ok := nextLocal(tl.selectableLocals(tl.sel, tl.lwidth()), tl.selLocal); ok {
		tl.selLocal = loc
		tl.scrollToSel()
		return
	}
	if tl.sel >= len(tl.items)-1 {
		tl.exitBrowse() // past the last item → resume follow
		return
	}
	tl.sel++
	tl.selLocal = 0
	tl.scrollToSel()
}

func (tl *timeline) cursorTop() {
	tl.enterBrowse()
	tl.sel = 0
	tl.selLocal = 0
	tl.scrollToSel()
}

func (tl *timeline) cursorBottom() { tl.exitBrowse() }

// --- line/page scroll (PgUp/PgDn, wheel) ---

func (tl *timeline) scrollLines(n int) {
	tl.enterBrowse()
	starts, ends, total := tl.layoutIndex(tl.lwidth())
	tl.scrollTop += n
	tl.clampScroll(total)
	tl.clampSelToView(starts, ends)
}

func (tl *timeline) pageUp() { tl.scrollLines(-tl.pageStep()) }

func (tl *timeline) pageDown() { tl.scrollDownOrExit(tl.pageStep()) }

func (tl *timeline) wheelUp() { tl.scrollLines(-browseLineStep) }

func (tl *timeline) wheelDown() { tl.scrollDownOrExit(browseLineStep) }

// scrollDownOrExit scrolls the browse viewport down by step lines, or resumes
// tail-follow when already at the bottom. It takes the layout total from
// layoutIndex — a single O(n) walk over cached heights — rather than a separate
// totalLines pass, and reuses the same starts/ends to keep the selection in view.
func (tl *timeline) scrollDownOrExit(step int) {
	if !tl.browsing {
		return // following the tail; nothing below
	}
	starts, ends, total := tl.layoutIndex(tl.lwidth())
	if tl.scrollTop >= maxOffset(total, tl.viewHeight) {
		tl.exitBrowse()
		return
	}
	tl.scrollTop += step
	tl.clampScroll(total)
	tl.clampSelToView(starts, ends)
}

// scrollToFraction jumps the viewport to a fraction [0,1] of the scroll range
// (the scrollbar-click entry point). At/near the bottom it resumes following.
func (tl *timeline) scrollToFraction(frac float64) {
	if frac >= 0.999 {
		tl.exitBrowse()
		return
	}
	tl.enterBrowse()
	if frac < 0 {
		frac = 0
	}
	starts, ends, total := tl.layoutIndex(tl.lwidth())
	tl.scrollTop = int(frac * float64(maxOffset(total, tl.viewHeight)))
	tl.clampScroll(total)
	tl.clampSelToView(starts, ends)
}

// toggleSelected expands/collapses the selected item if it's collapsible.
func (tl *timeline) toggleSelected() {
	if tl.sel < 0 || tl.sel >= len(tl.items) {
		return
	}
	if ht, ok := tl.items[tl.sel].(hitToggler); ok {
		if tg := ht.togglerAt(tl.lwidth(), tl.selLocal); tg != nil {
			tg.toggle()
			tl.clampSelLocal(tl.lwidth())
			return
		}
	}
	if tl.selLocal == 0 {
		if c, ok := tl.items[tl.sel].(collapsible); ok {
			c.toggle()
			tl.clampSelLocal(tl.lwidth())
		}
	}
}

// toggleAtViewportLine flips the [+]/[-] header on displayed viewport line vi
// (0-based from the top of the transcript pane), if that line bears one. It
// reads the line→item map recorded by the last render, so a click resolves
// against exactly what's on screen — in both tail and browse modes, and for a
// sub-entry header nested inside a group. Returns true when it toggled.
func (tl *timeline) toggleAtViewportLine(vi int) bool {
	if vi < 0 || vi >= len(tl.visItem) {
		return false
	}
	i := tl.visItem[vi]
	if i < 0 || i >= len(tl.items) {
		return false
	}
	ht, ok := tl.items[i].(hitToggler)
	if !ok {
		return false
	}
	tg := ht.togglerAt(tl.lwidth(), tl.visLocal[vi])
	if tg == nil {
		return false
	}
	tg.toggle()
	return true
}

// renderBrowse windows the timeline at scrollTop and rails the selected
// item. It emits only the lines inside [scrollTop, scrollTop+height) —
// walking just the items that intersect the window — so cost is
// O(viewport) string work instead of flattening the whole transcript.
// The line→item map for click hit-testing is recorded inline as lines
// are emitted, so there is no separate per-line scan. Used only while
// browsing — the streaming path stays on the bounded renderTail.
func (tl *timeline) renderBrowse(width, height int) []string {
	starts, ends, total := tl.layoutIndex(width)
	if total == 0 {
		return nil
	}
	if tl.scrollTop > total-height {
		tl.scrollTop = total - height
	}
	if tl.scrollTop < 0 {
		tl.scrollTop = 0
	}
	if tl.sel < 0 {
		tl.sel = 0
	}
	if tl.sel > len(tl.items)-1 {
		tl.sel = len(tl.items) - 1
	}
	top := tl.scrollTop
	end := min(top+height, total)

	n := end - top
	view := make([]string, 0, n)
	vItem := make([]int, 0, n)
	vLocal := make([]int, 0, n)
	emit := func(line string, itemIdx, local int) {
		view = append(view, line)
		vItem = append(vItem, itemIdx)
		vLocal = append(vLocal, local)
	}
	for i, it := range tl.items {
		sep := i > 0 && !itemNested(it)
		blockStart := starts[i]
		if sep {
			blockStart-- // the blank separator sits at starts[i]-1
		}
		if blockStart >= end {
			break // this item and every later one begin past the window
		}
		if ends[i] <= top {
			continue // ends before the window
		}
		if sep {
			if s := starts[i] - 1; s >= top && s < end {
				emit("", -1, 0)
			}
		}
		for k, ln := range tl.renderItem(it, width) {
			if abs := starts[i] + k; abs >= top && abs < end {
				emit(ln, i, k)
			}
		}
	}
	tl.visItem, tl.visLocal = vItem, vLocal

	tl.clampSelLocal(width)
	rail := palette.Primary.On("▎")
	ln := starts[tl.sel] + tl.selLocal
	if vi := ln - top; vi >= 0 && vi < len(view) {
		view[vi] = rail + view[vi]
	}
	return view
}
