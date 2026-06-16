package tui

// windowAroundCursor returns the [start,end) slice of n items that fits `rows`
// rows while keeping `cursor` visible — for plain lists (nav rails) that clip
// rather than draw "↑/↓ more" indicators. The cursor becomes the bottom row
// when scrolling down and the top row when scrolling up, so it never leaves the
// viewport. Callers map item i to screen row (i - start).
func windowAroundCursor(cursor, n, rows int) (int, int) {
	if rows < 1 || n <= 0 {
		return 0, 0
	}
	start := 0
	if cursor >= rows {
		start = cursor - rows + 1
	}
	if maxStart := n - rows; start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, min(start+rows, n)
}

// listWindow computes which slice [start,end) of n items to show in a viewport
// of `rows` rows while keeping `cursor` visible, plus whether to draw the
// "↑ more"/"↓ more" indicators (up/down). When the list overflows the viewport
// it reserves a row for each indicator that will be shown, so a scrolled cursor
// is never pushed off-screen by an indicator row — the bug where the selected
// item lands on (or past) the footer line instead of inside the list.
func listWindow(cursor, n, rows int) (int, int, bool, bool) {
	switch {
	case rows < 1 || n <= 0:
		return 0, 0, false, false
	case n <= rows:
		return 0, n, false, false // everything fits, no indicators
	case rows < 3:
		// Too short to fit an indicator alongside items — plain scroll, no
		// indicators, cursor kept visible.
		start := clampStart(cursor-rows+1, cursor, rows, n)
		return start, start + rows, false, false
	}
	// Overflow: reserve up to two rows for the indicators so the cursor stays in
	// view from any position. itemRows is the count of real item rows.
	itemRows := rows - 2
	start := clampStart(cursor-itemRows+1, cursor, itemRows, n)
	end := min(start+itemRows, n)
	return start, end, start > 0, end < n
}

// clampStart pins a tentative window start into [0, n-size], collapsing the
// cursor-relative offset bookkeeping shared by listWindow's branches.
func clampStart(start, cursor, size, n int) int {
	if cursor < size {
		start = 0
	}
	if maxStart := n - size; start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start
}
