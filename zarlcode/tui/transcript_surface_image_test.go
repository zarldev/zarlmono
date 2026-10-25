package tui_test

import (
	"regexp"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNativeTranscriptImageRespectsFixedInsets(t *testing.T) {
	ui, output := expandedNativeScreenshot(t, 1)
	ui.Update(tea.WindowSizeMsg{Width: 70, Height: 16})
	frame := output("\x1b[2J" + ui.View().Content)
	placement := regexp.MustCompile(`\x1b\[(\d+);(\d+)H\x1b_Ga=T,f=100,q=2,C=1,c=(\d+),r=(\d+)`).FindStringSubmatch(frame)
	if placement == nil {
		t.Fatal("missing native image placement")
	}
	x, _ := strconv.Atoi(placement[2])
	columns, _ := strconv.Atoi(placement[3])
	// Kitty coordinates are one-based. The pane's text lane begins after two
	// cells and ends before the right padding and reserved scrollbar column.
	if x < 3 || x+columns > 69 {
		t.Fatalf("native image escaped text lane: x=%d columns=%d", x, columns)
	}
}
