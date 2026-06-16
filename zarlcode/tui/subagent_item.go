package tui

import (
	"fmt"
	"strconv"
)

// subAgentItem folds a spawned sub-agent's entire run into a collapsible
// transcript block. When collapsed it shows a one-line summary; expanded
// it reveals the sub-agent's response, thinking, tools, and notices.
//
// It manages its own internal item tree (assistant turn, thinking, tool
// groups, notices) — the timeline routes Depth>0 events into the active
// subAgentItem instead of adding them directly to the flat items slice.
type subAgentItem struct {
	versioned
	depth     int
	nested    bool
	agentName string
	prompt    string // first line of the sub-agent prompt
	taskID    string
	children  []item
	expanded  bool
	closed    bool

	// Internal routing state — mirrors the timeline's own fields so
	// events can be routed into this sub-agent's item tree.
	curTools *groupItem
	curEdits *groupItem
	turn     *openTurn
	toolIdx  map[string]toolRef // local index for tools owned by this sub-agent
}

func newSubAgentItem(depth int, agentName, prompt, taskID string) *subAgentItem {
	return &subAgentItem{
		depth:     depth,
		nested:    true,
		agentName: agentName,
		prompt:    firstLine(prompt),
		taskID:    taskID,
		toolIdx:   make(map[string]toolRef),
	}
}

func (sa *subAgentItem) finished() bool { return sa.closed }

func (sa *subAgentItem) toggle() {
	sa.expanded = !sa.expanded
	sa.bump()
}

// childWidth is the render width for this sub-agent's children, which sit under
// the two-space gutter — the single source of this number, shared by render
// and togglerAt via renderChildBlock.
func (sa *subAgentItem) childWidth(width int) int { return width - 2 }

// togglerAt: line 0 is the sub-agent's own [+]/[-] header; when expanded, each
// child's header line toggles that child. Offsets come from the same
// renderChildBlock render uses. Returns nil for body lines and collapsed
// sub-agents.
func (sa *subAgentItem) togglerAt(width, ln int) toggler {
	if ln == 0 {
		return sa
	}
	if !sa.expanded {
		return nil
	}
	return renderChildBlock(sa.children, sa.childWidth(width)).togglerForLine(ln, sa.childWidth(width), sa.children, sa.bump)
}

func (sa *subAgentItem) render(width int) []string {
	toggle := palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("]")
	if sa.expanded {
		toggle = palette.Subtle.On("[") + palette.Primary.On("-") + palette.Subtle.On("]")
	}

	// Summary: agent name + prompt + stats
	summary := sa.agentName
	if sa.prompt != "" {
		summary += ": " + sa.prompt
	}
	toolCount := sa.toolCount()
	if toolCount > 0 {
		summary += fmt.Sprintf("  (%d %s)", toolCount, plural(toolCount, "tool", "tools"))
	}

	lines := []string{toggle + " " + palette.Subtle.On(summary)}
	if sa.expanded {
		lines = append(lines, renderChildBlock(sa.children, sa.childWidth(width)).lines...)
	}
	if sa.nested {
		lines = prefixLines(lines, nestPad)
	}
	return indentLines(lines, sa.depth)
}

func (sa *subAgentItem) toolCount() int {
	n := 0
	for _, c := range sa.children {
		if g, ok := c.(*groupItem); ok && g.kind == groupTools {
			n += len(g.children)
		}
	}
	return n
}

// --- Event routing methods (mirror timeline methods) ---

func (sa *subAgentItem) ensureTurn() *openTurn {
	if sa.turn != nil {
		return sa.turn
	}
	resp := &assistantItem{depth: sa.depth + 1}
	sa.children = append(sa.children, resp)
	sa.turn = &openTurn{resp: resp}
	return sa.turn
}

func (sa *subAgentItem) appendContent(delta string) {
	if delta == "" {
		return
	}
	ot := sa.ensureTurn()
	ot.resp.content += delta
	ot.resp.bump()
	sa.bump()
}

// appendThinking routes a reasoning delta from the runner's out-of-band
// Thinking channel into the sub-agent's thinking item.
func (sa *subAgentItem) appendThinking(delta string) {
	if delta == "" {
		return
	}
	ot := sa.ensureTurn()
	if ot.think == nil {
		ot.think = &thinkingItem{depth: sa.depth + 1, nested: true}
		sa.children = append(sa.children, ot.think)
	}
	ot.think.text += delta
	ot.think.bump()
	if ot.resp.content == "" {
		ot.resp.status = "thinking…"
		ot.resp.bump()
	}
	sa.bump()
}

func (sa *subAgentItem) startTool(toolID, name, arg string) {
	if ot := sa.turn; ot != nil && ot.resp.content == "" {
		ot.resp.status = "running " + name
		ot.resp.bump()
	}
	g := sa.ensureToolGroup()
	t := &toolItem{name: name, arg: arg, state: toolRunning}
	g.add(t)
	sa.toolIdx[toolID] = toolRef{group: g, tool: t}
	sa.bump()
}

func (sa *subAgentItem) addNotice(text string) {
	sa.children = append(sa.children, &noticeItem{depth: sa.depth + 1, text: text})
	sa.bump()
}

func (sa *subAgentItem) endTurn() {
	ot := sa.turn
	if ot == nil {
		return
	}
	if ot.think != nil {
		ot.think.done = true
		ot.think.bump()
	}
	ot.resp.done = true
	ot.resp.bump()
	sa.bump()
}

func (sa *subAgentItem) closeGroups() {
	for _, g := range []*groupItem{sa.curTools, sa.curEdits} {
		if g != nil {
			g.expanded = false
			g.closed = true
			g.bump()
		}
	}
	sa.curTools = nil
	sa.curEdits = nil
	sa.closed = true
	sa.bump()
}

func (sa *subAgentItem) ensureToolGroup() *groupItem {
	if sa.curTools == nil {
		g := &groupItem{depth: sa.depth + 1, kind: groupTools, nested: true}
		sa.children = append(sa.children, g)
		sa.curTools = g
	}
	return sa.curTools
}

// summaryLine returns the first line of the rendered output for use in
// the collapsed summary. Truncates at 120 chars.

// countLines is a helper for render tests.

// unused placeholder suppression
var _ = strconv.Itoa
