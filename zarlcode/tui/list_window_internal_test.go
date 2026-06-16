package tui

import "testing"

// The window must always contain the cursor — for every cursor position, list
// size, and viewport height — otherwise the selected item scrolls off-screen.
func TestListWindow_CursorAlwaysVisible(t *testing.T) {
	for _, rows := range []int{1, 2, 3, 5, 8, 20} {
		for _, n := range []int{0, 1, 3, 8, 50, 200} {
			for cursor := range n {
				start, end, up, down := listWindow(cursor, n, rows)
				if cursor < start || cursor >= end {
					t.Fatalf("cursor %d outside [%d,%d) (n=%d rows=%d)", cursor, start, end, n, rows)
				}
				// Total drawn rows (items + indicators) must fit the viewport.
				used := (end - start)
				if up {
					used++
				}
				if down {
					used++
				}
				if used > rows {
					t.Fatalf("uses %d rows > viewport %d (n=%d cursor=%d)", used, rows, n, cursor)
				}
				// A shown indicator must mean items are actually hidden on that side
				// (a false "more" arrow would be a lie). The reverse can fail only
				// on viewports too short for indicators (rows < 3).
				if up && start == 0 {
					t.Fatalf("↑ more shown but start=0 (n=%d cursor=%d rows=%d)", n, cursor, rows)
				}
				if down && end == n {
					t.Fatalf("↓ more shown but end=n (n=%d cursor=%d rows=%d)", n, cursor, rows)
				}
			}
		}
	}
}

// windowAroundCursor keeps the cursor inside the window for every position,
// size, and viewport height — the nav-rail invariant (no item ever clipped off
// the bottom of the rail).
func TestWindowAroundCursor_CursorAlwaysVisible(t *testing.T) {
	for _, rows := range []int{1, 2, 4, 10, 50} {
		for _, n := range []int{0, 1, 5, 50, 300} {
			for cursor := range n {
				start, end := windowAroundCursor(cursor, n, rows)
				if cursor < start || cursor >= end {
					t.Fatalf("cursor %d outside [%d,%d) (n=%d rows=%d)", cursor, start, end, n, rows)
				}
				if end-start > rows {
					t.Fatalf("window %d rows > viewport %d", end-start, rows)
				}
			}
		}
	}
}

// When everything fits, show all items and draw no indicators.
func TestListWindow_FitsShowsAll(t *testing.T) {
	start, end, up, down := listWindow(2, 5, 8)
	if start != 0 || end != 5 || up || down {
		t.Errorf("fit case: got [%d,%d) up=%v down=%v, want [0,5) no indicators", start, end, up, down)
	}
}

// At the top no ↑ indicator; at the bottom no ↓ indicator.
func TestListWindow_Edges(t *testing.T) {
	if _, _, up, _ := listWindow(0, 50, 8); up {
		t.Error("cursor at top should not draw ↑ more")
	}
	if _, end, _, down := listWindow(49, 50, 8); down || end != 50 {
		t.Errorf("cursor at bottom should reach end and not draw ↓ more (end=%d down=%v)", end, down)
	}
}
