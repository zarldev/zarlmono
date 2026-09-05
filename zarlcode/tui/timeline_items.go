package tui

import (
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// item is one render unit. render returns wrapped lines for width; version is
// the owner's complete inline-render revision; finished reports terminal state.
type item interface {
	render(width int) []string
	version() uint64
	finished() bool
}

// versioned supplies version()/bump() to mutable items via embedding.
type versioned struct{ v uint64 }

func (x *versioned) version() uint64 { return x.v }
func (x *versioned) bump()           { x.v++ }

// cacheEntry holds an item's rendered lines for one (width, version, theme).
type cacheEntry struct {
	width int
	ver   uint64
	gen   uint64 // themeGen the lines were rendered under
	lines []string
}

// --- item types ---

type userItem struct {
	versioned
	text string
}

func (u *userItem) render(width int) []string {
	anchor, rail := turnOwnerPrefixes("you", palette.User.On)
	lines := []string{anchor}
	lines = append(lines, renderPlain(width, u.text, withFirstPrefix(rail, rail))...)
	return append(lines, palette.User.On("└─"))
}
func (u *userItem) finished() bool { return true }

type queuedUserItem struct {
	versioned
	text     string
	injected bool
}

func (q *queuedUserItem) render(width int) []string {
	anchor, rail := turnOwnerPrefixes("you", palette.User.On)
	if !q.injected {
		anchor += palette.Muted.On("  ◷ queued")
	}
	lines := []string{anchor}
	lines = append(lines, renderPlain(width, q.text, withFirstPrefix(rail, rail))...)
	if q.injected {
		lines = append(lines, palette.User.On("└─"))
	}
	return lines
}
func (q *queuedUserItem) finished() bool { return q.injected }

type assistantItem struct {
	versioned
	depth       int
	content     string // accumulated visible answer (the turn headline)
	status      string // live placeholder shown while content == "" (e.g. "working…")
	done        bool
	interrupted bool
	md          streamingMarkdown
	hasActivity bool // supporting activity follows the response body
}

func (a *assistantItem) render(width int) []string {
	if a.content == "" {
		status := a.status
		if a.interrupted {
			status = "interrupted"
		}
		if status == "" {
			status = "working…"
		}
		anchor, rail := turnOwnerPrefixes("zarl", palette.Assistant.On)
		anchor += palette.Muted.On("  " + status)
		return []string{anchor, rail}
	}
	anchor, rail := turnOwnerPrefixes("zarl", palette.Assistant.On)
	lines := []string{anchor}
	lines = append(lines, renderContentBlock(width, contentBlock{
		kind: contentMarkdown, text: a.content, depth: a.depth, markdown: &a.md,
		firstPrefix: rail, continuationPrefix: rail,
	})...)
	if a.interrupted {
		lines = append(lines, rail+palette.Warning.On("[interrupted]"))
	}
	if a.hasActivity {
		lines = append(lines, palette.Assistant.On("├─"))
	}
	return lines
}
func (a *assistantItem) finished() bool { return a.done }

type toolState int

const (
	toolRunning toolState = iota
	toolOK
	toolFailed
	toolInterrupted
)

type toolItem struct {
	versioned
	depth          int
	name           string
	arg            string // compact tool-specific argument hint
	effect         string // compact post-action effect summary
	state          toolState
	failKind       tools.Kind // failure classification; only meaningful when state == toolFailed
	waiting        bool
	waitAccess     tools.WorkspaceAccess
	waitPaths      []string
	waitDuration   time.Duration
	result         string // full formatted output (or error); shown when expanded
	data           any    // typed structured result (code.GrepResult, …); nil = render from result string
	dur            time.Duration
	sequence       int
	expanded       bool // result shown ([-]) vs hidden ([+]); only meaningful once result != ""
	children       []*toolItem
	layout         childBlockCache
	layoutChildren []item
	notify         func()
}

func (t *toolItem) bump() {
	t.versioned.bump()
	if t.notify != nil {
		t.notify()
	}
}

func (t *toolItem) childItems() []item {
	if len(t.layoutChildren) != len(t.children) {
		t.layoutChildren = make([]item, len(t.children))
	}
	for i, child := range t.children {
		t.layoutChildren[i] = child
	}
	return t.layoutChildren
}

func (t *toolItem) childSummary() string {
	if len(t.children) == 0 {
		return ""
	}
	done, failed, interrupted := 0, 0, 0
	for _, child := range t.children {
		if child.state != toolRunning {
			done++
		}
		switch child.state {
		case toolFailed:
			failed++
		case toolInterrupted:
			interrupted++
		}
	}
	summary := fmt.Sprintf("%d/%d calls", done, len(t.children))
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
	}
	if interrupted > 0 {
		summary += fmt.Sprintf(", %d interrupted", interrupted)
	}
	return summary
}

func (t *toolItem) render(width int) []string {
	icon := palette.Warning.On("◌")
	switch t.state {
	case toolOK:
		icon = palette.Success.On("✓")
	case toolFailed:
		icon = palette.Error.On("✗")
	case toolInterrupted:
		icon = palette.Warning.On("■")
	}
	head := icon + " " + t.name
	if t.state == toolInterrupted {
		head += " " + palette.Warning.On("interrupted")
	}
	if t.state == toolRunning {
		if t.waiting {
			head += " " + palette.Warning.On("waiting")
			if summary := workspaceWaitSummary(t.waitAccess, t.waitPaths); summary != "" {
				head += "  " + palette.Muted.On("· "+summary)
			}
		} else {
			head += " " + palette.Warning.On("running")
		}
	}
	if t.state == toolFailed && t.failKind != tools.Kinds.UNKNOWN {
		head += " " + kindBadge(t.failKind)
	}
	if t.arg != "" {
		head += "  " + t.arg
	}
	if summary := t.childSummary(); summary != "" {
		head += "  " + palette.Muted.On("· "+summary)
	}
	if t.effect != "" {
		head += "  " + palette.Muted.On("· "+t.effect)
	}
	if t.dur > 0 {
		head += "  (" + t.dur.Round(time.Millisecond).String() + ")"
	}
	// Prefix a clickable disclosure when the tool has output or nested calls. For
	// program, the disclosure hides/shows the nested call list; each child row can
	// then open its own result independently.
	hasDisclosure := t.result != "" || len(t.children) > 0
	if hasDisclosure {
		glyph := palette.Subtle.On("[") + palette.Primary.On("-") + palette.Subtle.On("] ")
		if !t.expanded {
			glyph = palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("] ")
		}
		head = glyph + head
	}
	bodyLines := t.resultBodyLines(width)
	lines := []string{head}
	if len(bodyLines) > 0 {
		lines = append(lines, bodyLines...)
	}
	if t.expanded {
		lines = append(lines, t.layout.render(t.childItems(), width, t.version()).rawLines...)
	}
	return indentLines(lines, t.depth)
}

// resultBodyLines returns the result rows rendered between a tool's disclosure
// header and any nested child calls. Hit-testing uses the same rows so deeply
// nested disclosure coordinates cannot drift from rendering.
func (t *toolItem) resultBodyLines(width int) []string {
	if t.result == "" || !t.expanded {
		return nil
	}
	if t.suppressesResultBody() {
		// Program results only repeat the child-call summary already present in
		// the header. Keep the children directly beneath that header so render,
		// keyboard navigation, and mouse hit-testing share contiguous rows.
		return nil
	}
	return renderToolResult(width-t.depth*2, t.name, t.arg, t.result,
		withBodyPrefix("    "),
		withMaxLines(toolResultMaxLines),
		withData(t.data),
	)
}

func (t *toolItem) suppressesResultBody() bool {
	return t.name == "program" && len(t.children) > 0 && t.childSummary() != ""
}

func (t *toolItem) togglerAt(width, ln int) toggler {
	if ln == 0 {
		return t
	}
	if !t.expanded || len(t.children) == 0 {
		return nil
	}
	children := t.childItems()
	bodyLines := len(t.resultBodyLines(width))
	return t.layout.render(children, width, t.version()).togglerForLine(ln-bodyLines, width, children, t.bump)
}

func (t *toolItem) toggleLocals(width int) []int {
	if !t.expanded || len(t.children) == 0 {
		return []int{0}
	}
	children := t.childItems()
	block := t.layout.render(children, width, t.version())
	locals := append([]int{0}, block.toggleLocals(width, children)...)
	bodyLines := len(t.resultBodyLines(width))
	for i := 1; i < len(locals); i++ {
		locals[i] += bodyLines
	}
	return locals
}
func (t *toolItem) finished() bool { return t.state != toolRunning }

// kindBadge renders a tool failure's typed classification as a small colored
// "[label]" — warning for caller-fixable classes (validation / not_found /
// permission), error for fatal, muted otherwise. The renderer, not the event,
// owns this presentation decision.
func kindBadge(k tools.Kind) string {
	col := palette.Muted
	switch k {
	case tools.Kinds.VALIDATION, tools.Kinds.NOTFOUND, tools.Kinds.PERMISSION:
		col = palette.Warning
	case tools.Kinds.FATAL:
		col = palette.Error
	}
	return col.On("[" + k.String() + "]")
}

// toggle flips the tool row's result drawer. Clicked through its parent group,
// which bumps so the inline re-render picks up the new state.
func (t *toolItem) toggle() { t.expanded = !t.expanded; t.bump() }

type noticeItem struct {
	versioned
	depth int
	text  string
}

func (n *noticeItem) render(width int) []string {
	return renderPlain(width, n.text, withDepth(n.depth))
}
func (n *noticeItem) finished() bool { return true }
