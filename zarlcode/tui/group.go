package tui

import "fmt"

type groupKind int

const (
	groupTools groupKind = iota
	groupEdits
	groupAgents
)

type versionNotifier func()

func notifyParent(local func(), parent versionNotifier) versionNotifier {
	return func() {
		local()
		if parent != nil {
			parent()
		}
	}
}

// groupItem owns an iteration's activity: collapsed is summary-only, expanded
// exposes every child. Individual calls retain their own evidence disclosures.
type groupItem struct {
	versioned
	depth    int
	nested   bool // indented under the turn's response
	kind     groupKind
	children []item
	expanded bool
	layout   childBlockCache
	notify   func()
	closed   bool
}

func (g *groupItem) finished() bool { return g.closed }
func (g *groupItem) bump() {
	g.versioned.bump()
	if g.notify != nil {
		g.notify()
	}
}

func (g *groupItem) toggle() {
	g.expanded = !g.expanded
	g.bump()
}

func (g *groupItem) add(it item) {
	g.children = append(g.children, it)
	g.bump()
}

// childWidth shares the single activity indent with text and image placement.
func (g *groupItem) childWidth(width int) int { return width - len(childIndent) }

// visibleChildren is shared by text, disclosure navigation and native images.
// Its borrowed item identities retain their original execution/detail state.
func (g *groupItem) visibleChildren() []item {
	if g.expanded {
		return g.children
	}
	return nil
}

// togglerAt maps a local line within the group's rendered block to its toggle:
// line 0 is the group's own [+]/[-]; when expanded, each child's header line
// toggles that child. Offsets come from the same renderChildBlock render uses,
// so the two can't drift. Returns nil for body lines and collapsed groups.
func (g *groupItem) togglerAt(width, ln int) toggler {
	if ln == 0 {
		return g
	}
	children := g.visibleChildren()
	childWidth := g.childWidth(width)
	return g.layout.render(children, childWidth, g.version()).togglerForLine(ln, childWidth, children, g.bump)
}

func (g *groupItem) toggleLocals(width int) []int {
	children := g.visibleChildren()
	childWidth := g.childWidth(width)
	locals := g.layout.render(children, childWidth, g.version()).toggleLocals(childWidth, children)
	return append([]int{0}, locals...)
}

func (g *groupItem) render(width int) []string {
	toggle := palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("]")
	if g.expanded {
		toggle = palette.Subtle.On("[") + palette.Primary.On("-") + palette.Subtle.On("]")
	}
	children := g.visibleChildren()
	head := toggle + " " + palette.Muted.On(g.summary())
	lines := []string{head}
	lines = append(lines, g.layout.render(children, g.childWidth(width), g.version()).lines...)
	return indentLines(lines, g.depth)
}

func (g *groupItem) summary() string {
	switch g.kind {
	case groupEdits:
		return fmt.Sprintf("edits (%d %s)", len(g.children), plural(len(g.children), "file", "files"))
	case groupAgents:
		interrupted := 0
		for _, child := range g.children {
			if agent, ok := child.(*subAgentItem); ok && agent.interrupted {
				interrupted++
			}
		}
		summary := fmt.Sprintf("agents (%d)", len(g.children))
		if interrupted > 0 {
			summary += fmt.Sprintf("  %d interrupted", interrupted)
		}
		return summary
	}
	failed, interrupted := 0, 0
	for _, c := range g.children {
		if t, ok := c.(*toolItem); ok {
			switch t.state {
			case toolFailed:
				failed++
			case toolInterrupted:
				interrupted++
			}
		}
	}
	s := fmt.Sprintf("tools (%d)", len(g.children))
	if failed > 0 {
		s += fmt.Sprintf("  %d failed", failed)
	}
	if interrupted > 0 {
		s += fmt.Sprintf("  %d interrupted", interrupted)
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
		g := &groupItem{depth: depth, kind: groupTools, nested: true}
		g.notify = func() { tl.invalidateItem(g) }
		tl.appendItem(g)
		tl.curTools = g
	}
	return tl.curTools
}

func (tl *timeline) ensureEditGroup(depth int) *groupItem {
	if tl.curEdits == nil {
		g := &groupItem{depth: depth, kind: groupEdits, nested: true}
		g.notify = func() { tl.invalidateItem(g) } // under the response, collapsed
		tl.appendItem(g)
		tl.curEdits = g
	}
	return tl.curEdits
}

// closeGroups freezes the current iteration without discarding tool inspection
// state. The next iteration opens fresh groups, including for active sub-agents.
func (tl *timeline) closeGroups() {
	for _, g := range []*groupItem{tl.curTools, tl.curEdits} {
		if g != nil {
			if g.kind != groupTools {
				g.expanded = false
			}
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
