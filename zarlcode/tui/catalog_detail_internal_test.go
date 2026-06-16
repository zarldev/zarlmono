package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// When the agent/skill list overflows the detail height, detailLines windows
// around the cursor so the focused row stays visible (and the top scrolls off).
func TestCatalogPane_DetailLinesWindowsCursor(t *testing.T) {
	p := &catalogPane{noun: "agent"}
	for i := range 40 {
		p.rows = append(p.rows, catalogRow{name: fmt.Sprintf("agent-%02d", i)})
	}
	p.cursor = len(p.rows) - 1 // focus the last row

	out := ansi.Strip(strings.Join(p.detailLines(80, 12), "\n"))
	if !strings.Contains(out, "agent-39") {
		t.Errorf("focused row must stay visible when the list overflows:\n%s", out)
	}
	if strings.Contains(out, "agent-00") {
		t.Errorf("top of an overflowing list should be windowed out:\n%s", out)
	}
}

// An expanded row anchors near the top so its body has room below — and stays
// visible regardless of where it sits in a long list.
func TestCatalogPane_DetailLinesExpandedAnchors(t *testing.T) {
	p := &catalogPane{noun: "skill", expanded: true}
	for i := range 40 {
		p.rows = append(p.rows, catalogRow{name: fmt.Sprintf("skill-%02d", i), body: "the body"})
	}
	p.cursor = 30

	out := ansi.Strip(strings.Join(p.detailLines(80, 12), "\n"))
	if !strings.Contains(out, "skill-30") {
		t.Errorf("expanded focused row must be visible:\n%s", out)
	}
	if !strings.Contains(out, "the body") {
		t.Errorf("expanded row's body should render below it:\n%s", out)
	}
}

// A list that fits renders every row from the top, unchanged.
func TestCatalogPane_DetailLinesSmallListUnchanged(t *testing.T) {
	p := &catalogPane{noun: "agent", cursor: 1, rows: []catalogRow{{name: "alpha"}, {name: "beta"}}}
	out := ansi.Strip(strings.Join(p.detailLines(80, 12), "\n"))
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("a list that fits should render all rows:\n%s", out)
	}
}
