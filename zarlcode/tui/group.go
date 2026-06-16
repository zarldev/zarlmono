package tui

import "fmt"

type groupKind int

const (
	groupTools groupKind = iota
	groupEdits
)

// groupItem folds an iteration's tool calls (or edits) into one
// collapsible block: a summary line, plus the child rows when expanded.
// Collapsed by default (just the summary line); browse + enter expands
// it. closeGroups freezes it when the iteration ends.
type groupItem struct {
	versioned
	depth    int
	nested   bool // indented under the turn's response
	kind     groupKind
	children []item
	expanded bool
	closed   bool
}

func (g *groupItem) finished() bool { return g.closed }

func (g *groupItem) toggle() {
	g.expanded = !g.expanded
	g.bump()
}

func (g *groupItem) add(it item) {
	g.children = append(g.children, it)
	g.bump()
}

// childWidth is the render width for this group's children. Children sit under
// the two-space gutter, and the group historically reserves four columns for
// them — the single source of this number, shared by render, togglerAt, and
// selectableLocals via renderChildBlock.
func (g *groupItem) childWidth(width int) int { return width - 4 }

// togglerAt maps a local line within the group's rendered block to its toggle:
// line 0 is the group's own [+]/[-]; when expanded, each child's header line
// toggles that child. Offsets come from the same renderChildBlock render uses,
// so the two can't drift. Returns nil for body lines and collapsed groups.
func (g *groupItem) togglerAt(width, ln int) toggler {
	if ln == 0 {
		return g
	}
	if !g.expanded {
		return nil
	}
	return renderChildBlock(g.children, g.childWidth(width)).togglerForLine(ln, g.childWidth(width), g.children, g.bump)
}

func (g *groupItem) render(width int) []string {
	toggle := palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("]")
	if g.expanded {
		toggle = palette.Subtle.On("[") + palette.Primary.On("-") + palette.Subtle.On("]")
	}
	lines := []string{toggle + " " + palette.Subtle.On(g.summary())}
	if g.expanded {
		lines = append(lines, renderChildBlock(g.children, g.childWidth(width)).lines...)
	}
	if g.nested {
		lines = prefixLines(lines, nestPad)
	}
	return indentLines(lines, g.depth)
}

func (g *groupItem) summary() string {
	if g.kind == groupEdits {
		return fmt.Sprintf("edits (%d %s)", len(g.children), plural(len(g.children), "file", "files"))
	}
	failed := 0
	for _, c := range g.children {
		if t, ok := c.(*toolItem); ok && t.state == toolFailed {
			failed++
		}
	}
	s := fmt.Sprintf("tools (%d)", len(g.children))
	if failed > 0 {
		s += fmt.Sprintf("  %d failed", failed)
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// toolRef locates a tool call (its row + the group it lives in) so
// finishTool can update it and bump the right timeline item.
type toolRef struct {
	group *groupItem
	tool  *toolItem
}

func (tl *timeline) ensureToolGroup(depth int) *groupItem {
	if tl.curTools == nil {
		g := &groupItem{depth: depth, kind: groupTools, nested: true} // under the response, collapsed
		tl.items = append(tl.items, g)
		tl.curTools = g
	}
	return tl.curTools
}

func (tl *timeline) ensureEditGroup(depth int) *groupItem {
	if tl.curEdits == nil {
		g := &groupItem{depth: depth, kind: groupEdits, nested: true} // under the response, collapsed
		tl.items = append(tl.items, g)
		tl.curEdits = g
	}
	return tl.curEdits
}

// closeGroups ends the current iteration's groups: collapse them to
// summaries and freeze. The next tool/edit opens fresh groups, giving
// per-iteration grouping. Also closes groups for any active sub-agents.
func (tl *timeline) closeGroups() {
	for _, g := range []*groupItem{tl.curTools, tl.curEdits} {
		if g != nil {
			g.expanded = false
			g.closed = true
			g.bump()
		}
	}
	tl.curTools = nil
	tl.curEdits = nil
	// Also close sub-agent groups at the same boundary.
	for _, sa := range tl.subAgents {
		sa.closeGroups()
	}
}
