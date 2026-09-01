package tui

import (
	"fmt"
	"strings"
	"time"

	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// timeline is the run rendered as a vertical list of items — user
// prompts, assistant turns, tool calls, notices. It is mutated by the
// runner-event handlers in Update (see handleRunnerMsg) and rendered
// tail-first into the main pane.
//
// Rendering is cached and viewport-bounded: each item caches
// its lines per (width, version); items that are finished keep a stable
// version and render exactly once per width (freeze); and a frame only
// renders enough items from the bottom to fill the viewport, so cost is
// O(viewport) regardless of history length.
type timeline struct {
	items           []item
	toolIdx         map[string]toolRef            // ToolID -> its row + the group it lives in
	turns           map[string]*openTurn          // TaskID -> in-progress turn (split think/answer)
	cache           map[item]cacheEntry           // per-item render cache, keyed (width, version)
	pendingChildren map[string][]pendingToolChild // Parent ToolID -> children that arrived first
	queued          []*queuedUserItem             // FIFO user inputs waiting for SteerInjected

	// subAgents tracks in-progress sub-agent runs by TaskID. Depth>0
	// events route into the matching subAgentItem instead of the flat
	// items slice, so each spawned agent gets its own collapsible block.
	subAgents        map[string]*subAgentItem // child TaskID -> active item
	subAgentsBySpawn map[string]*subAgentItem // agent_spawn ToolID -> reserved/correlated item
	agents           *groupItem

	// curTools/curEdits are the open per-iteration groups (nil = none);
	// reset by closeGroups so each iteration starts fresh groups.
	curTools *groupItem
	curEdits *groupItem

	// Browse/scrollback state. browsing=false follows the tail
	// (auto-scroll, streaming); browsing=true freezes on cursor (the
	// selected item) for scrollback + collapse. scrollTop is the persistent
	// viewport offset (top line) while browsing — held across renders and
	// nudged only enough to keep the cursor item visible, so moving the
	// selection scrolls smoothly instead of snapping the cursor to the top.
	browsing  bool
	scrollTop int // viewport top line while browsing
	sel       int // selected item index (the expand target); arrow keys move it
	selLocal  int // selected local line within sel; lets keyboard reach group children
	// View metrics cached from the last render so navigation and the
	// scrollbar can clamp/scroll without re-measuring the layout.
	viewWidth  int
	viewHeight int

	// visItem/visLocal map each displayed viewport line (by index) back to the
	// item it came from (index into items, or -1 for a blank separator) and the
	// local line within that item. Recorded every render so a mouse click can
	// resolve the row under the cursor to a [+]/[-] toggle — see
	// toggleAtViewportLine.
	visItem    []int
	visLocal   []int
	tailBlocks []timelineRenderBlock

	selection transcriptSelection

	geometry timelineGeometry
}

// timelineGeometry is the timeline-owned measured layout for one ordered item
// slice at a width and theme generation. dirty is the earliest item whose
// height or following prefix offsets must be rebuilt.
type timelineGeometry struct {
	width     int
	gen       uint64
	items     []item
	vers      []uint64
	heights   []int
	starts    []int
	ends      []int
	total     int
	dirty     int
	positions map[item]int
}

func (g *timelineGeometry) invalidateFrom(index int) {
	if index < 0 {
		index = 0
	}
	if g.dirty > index {
		g.dirty = index
	}
}

func (g *timelineGeometry) clear() { *g = timelineGeometry{dirty: 0} }

func (g *timelineGeometry) rebuildPositions(items []item) {
	g.positions = make(map[item]int, len(items))
	for i, it := range items {
		g.positions[it] = i
	}
}

type timelineRenderBlock struct {
	idx   int
	it    item
	lines []string
}

type pendingToolChild struct {
	toolID   string
	sequence int
	tool     *toolItem
}

// Clear resets the timeline to empty, discarding all items, turns, caches,
// and queued inputs. Does not affect the current run — only the transcript.
func (tl *timeline) Clear() {
	tl.clearItems()
	tl.toolIdx = make(map[string]toolRef)
	tl.pendingChildren = make(map[string][]pendingToolChild)
	tl.turns = make(map[string]*openTurn)
	tl.cache = make(map[item]cacheEntry)
	tl.queued = nil
	tl.subAgents = make(map[string]*subAgentItem)
	tl.subAgentsBySpawn = make(map[string]*subAgentItem)
	tl.agents = nil
	tl.curTools = nil
	tl.curEdits = nil
	tl.browsing = false
	tl.scrollTop = 0
	tl.sel = 0
	tl.selLocal = 0
	tl.visItem = nil
	tl.visLocal = nil
}

func newTimeline() *timeline {
	return &timeline{
		toolIdx:          make(map[string]toolRef),
		pendingChildren:  make(map[string][]pendingToolChild),
		turns:            make(map[string]*openTurn),
		cache:            make(map[item]cacheEntry),
		subAgents:        make(map[string]*subAgentItem),
		subAgentsBySpawn: make(map[string]*subAgentItem),
		geometry:         timelineGeometry{dirty: 0},
	}
}

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
	md          streamingMarkdown
	hasActivity bool // supporting activity follows the response body
}

func (a *assistantItem) render(width int) []string {
	if a.content == "" {
		status := a.status
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
	done, failed := 0, 0
	for _, child := range t.children {
		if child.state != toolRunning {
			done++
		}
		if child.state == toolFailed {
			failed++
		}
	}
	summary := fmt.Sprintf("%d/%d calls", done, len(t.children))
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed", failed)
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
	}
	head := icon + " " + t.name
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

// --- mutation (driven by runner events) ---

// pushItem appends an item and keeps any queued user items pinned to the tail
// clearItems drops the transcript structure and its owned geometry.
func (tl *timeline) clearItems() {
	tl.items = nil
	tl.geometry.clear()
}

// appendItem is the only top-level append path.
func (tl *timeline) appendItem(it item) {
	tl.items = append(tl.items, it)
	if tl.geometry.positions == nil {
		tl.geometry.positions = make(map[item]int)
	}
	tl.geometry.positions[it] = len(tl.items) - 1
	tl.geometry.invalidateFrom(len(tl.items) - 1)
}

// reorderItems records that ordering changed before replacing the top-level slice.
func (tl *timeline) reorderItems(items []item, firstChanged int) {
	tl.items = items
	tl.geometry.rebuildPositions(items)
	tl.geometry.invalidateFrom(firstChanged)
}

// invalidateItem records a known top-level mutation. Nested owners notify their
// containing top-level item through this callback.
func (tl *timeline) invalidateItem(changed item) {
	if i, ok := tl.geometry.positions[changed]; ok {
		tl.geometry.invalidateFrom(i)
	}
}

func (tl *timeline) geometryIndex(width int) ([]int, []int, int) {
	g := &tl.geometry
	if g.width != width || g.gen != themeGen {
		g.width, g.gen, g.dirty = width, themeGen, 0
	}
	if g.dirty > len(tl.items) {
		g.dirty = len(tl.items)
	}
	// Top-level transitions invalidate their owner directly, so stable reads
	// never scan history merely to prove that it has not changed.
	if g.dirty < len(tl.items) {
		if cap(g.items) < len(tl.items) {
			g.items = make([]item, len(tl.items))
			g.vers = make([]uint64, len(tl.items))
			g.heights = make([]int, len(tl.items))
			g.starts = make([]int, len(tl.items))
			g.ends = make([]int, len(tl.items))
		} else {
			g.items = g.items[:len(tl.items)]
			g.vers = g.vers[:len(tl.items)]
			g.heights = g.heights[:len(tl.items)]
			g.starts = g.starts[:len(tl.items)]
			g.ends = g.ends[:len(tl.items)]
		}
		start := 0
		if g.dirty > 0 {
			start = g.ends[g.dirty-1]
		}
		for i := g.dirty; i < len(tl.items); i++ {
			if i > 0 && !itemNested(tl.items[i]) {
				start++
			}
			g.items[i] = tl.items[i]
			g.vers[i] = tl.items[i].version()
			g.starts[i] = start
			g.heights[i] = len(tl.renderItem(tl.items[i], width))
			start += g.heights[i]
			g.ends[i] = start
		}
		g.total, g.dirty = start, len(tl.items)
	}
	return g.starts, g.ends, g.total
}

// so they always render below streaming content that arrived while waiting.
func (tl *timeline) pushItem(it item) {
	tl.appendItem(it)
	if len(tl.queued) == 0 {
		return
	}
	// Collect the current queued set.
	qset := make(map[item]bool, len(tl.queued))
	for _, q := range tl.queued {
		qset[q] = true
	}
	// Filter out queued items, preserving non-queued order.
	kept := tl.items[:0]
	for _, it := range tl.items {
		if !qset[it] {
			kept = append(kept, it)
		}
	}
	// Re-append queued items at the tail in FIFO order.
	for _, q := range tl.queued {
		kept = append(kept, q)
	}
	tl.reorderItems(kept, 0)
}

func (tl *timeline) addUser(text string) {
	tl.pushItem(&userItem{text: text})
}

func (tl *timeline) addQueuedUser(text string) {
	q := &queuedUserItem{text: text}
	tl.appendItem(q)
	tl.queued = append(tl.queued, q)
}

func (tl *timeline) addInjectedUser(text string) {
	if len(tl.queued) == 0 {
		tl.addUser(text)
		return
	}
	q := tl.queued[0]
	tl.queued = tl.queued[1:]
	q.text = text
	q.injected = true
	q.bump()
	// Move to the bottom of the transcript so the injected input appears after
	// any tool calls or streaming content that arrived while it was queued.
	for i, it := range tl.items {
		if it == q {
			top := append([]item(nil), tl.items[:i]...)
			top = append(top, tl.items[i+1:]...)
			top = append(top, q)
			tl.reorderItems(top, i)
			break
		}
	}
}

func (tl *timeline) addNotice(text string) {
	tl.pushItem(&noticeItem{depth: 0, text: text})
}

// reserveSubAgent creates the visible transcript row as soon as agent_spawn
// starts. The child TaskID does not exist yet, so ConversationStarted later
// binds the reserved row through ParentToolCallID.
func (tl *timeline) reserveSubAgent(spawnToolID string, depth int, agentName, prompt string) *subAgentItem {
	if spawnToolID != "" {
		if sa := tl.subAgentsBySpawn[spawnToolID]; sa != nil {
			return sa
		}
	}
	if tl.agents == nil {
		tl.agents = &groupItem{kind: groupAgents, nested: true, expanded: spawnToolID != ""}
		tl.agents.notify = func() { tl.invalidateItem(tl.agents) }
		tl.pushItem(tl.agents)
	}
	sa := newPendingSubAgentItem(depth+1, agentName, prompt, spawnToolID)
	sa.depth = 0
	sa.notify = tl.agents.bump
	tl.agents.add(sa)
	if spawnToolID != "" {
		tl.subAgentsBySpawn[spawnToolID] = sa
	}
	return sa
}

// startSubAgentWithParent binds the child run to the row reserved by its exact
// agent_spawn call. Falling back to a new row keeps replayed/legacy event
// streams that lack ParentToolCallID visible.
func (tl *timeline) startSubAgentWithParent(taskID string, depth int, agentName, prompt, spawnToolID string) *subAgentItem {
	if sa := tl.subAgents[taskID]; sa != nil {
		return sa
	}
	var sa *subAgentItem
	if spawnToolID != "" {
		sa = tl.subAgentsBySpawn[spawnToolID]
	}
	if sa == nil {
		sa = tl.reserveSubAgent(spawnToolID, depth-1, agentName, prompt)
	}
	sa.bind(taskID, depth, agentName, prompt)
	tl.subAgents[taskID] = sa
	return sa
}

// finishSubAgent finalizes the sub-agent run: closes its internal groups,
// marks it closed, and removes it from the active sub-agents map so future
// events for this taskID don't accidentally route to a finished item.
func (tl *timeline) finishSubAgent(taskID string) {
	sa := tl.subAgents[taskID]
	if sa == nil {
		return
	}
	sa.endTurn()
	sa.closeGroups()
	delete(tl.subAgents, taskID)
}

func (tl *timeline) subAgent(taskID string) *subAgentItem {
	return tl.subAgents[taskID]
}

// failSubAgentSpawn leaves a terminal box in the transcript when validation or
// admission fails before a child ConversationStarted event can be published.
func (tl *timeline) failSubAgentSpawn(spawnToolID, detail string) {
	if sa := tl.subAgentsBySpawn[spawnToolID]; sa != nil {
		sa.failLaunch(detail)
	}
}

// addLoadedSkill records a successfully loaded skill under the given turn.
// The skillsItem is always created at turn start; this just populates it.
func (tl *timeline) addLoadedSkill(taskID, name string) {
	ot := tl.turns[taskID]
	if ot == nil || ot.skills == nil {
		return
	}
	tl.markTurnActivity(ot)
	ot.skills.add(name)
	tl.invalidateItem(ot.skills)
}

// appendContent grows the open assistant item for taskID, creating one
// if the previous turn segment was closed by a tool call or notice.
// startTurn opens an assistant turn: a response headline (placeholder
// until content streams) under which the turn's thinking + tool activity
// renders. Called eagerly at ConversationStarted so the response sits on
// top of its activity.
func (tl *timeline) startTurn(taskID string, depth int) *openTurn {
	resp := &assistantItem{depth: depth}
	tl.pushItem(resp)
	skills := &skillsItem{nested: true}
	tl.pushItem(skills)
	ot := &openTurn{resp: resp, skills: skills}
	tl.turns[taskID] = ot
	return ot
}

func (tl *timeline) markTurnActivity(ot *openTurn) {
	if ot == nil || ot.resp == nil || ot.resp.hasActivity {
		return
	}
	ot.resp.hasActivity = true
	ot.resp.bump()
	tl.invalidateItem(ot.resp)
}

func (tl *timeline) ensureTurn(taskID string, depth int) *openTurn {
	if ot := tl.turns[taskID]; ot != nil {
		return ot
	}
	return tl.startTurn(taskID, depth)
}

func (tl *timeline) appendContent(taskID string, depth int, delta string) {
	if sa := tl.subAgents[taskID]; sa != nil {
		sa.appendContent(delta)
		return
	}
	if delta == "" {
		return
	}
	ot := tl.ensureTurn(taskID, depth)
	ot.resp.content += delta
	ot.resp.bump()
	tl.invalidateItem(ot.resp)
}

// appendThinking routes a reasoning delta from the runner's out-of-band
// Thinking channel straight to the turn's thinking item. Every provider
// (Anthropic extended thinking, DeepSeek/OpenAI reasoning_content,
// Gemini thought parts) lands here.
func (tl *timeline) appendThinking(taskID string, depth int, delta string) {
	if delta == "" {
		return
	}
	if sa := tl.subAgents[taskID]; sa != nil {
		sa.appendThinking(delta)
		return
	}
	ot := tl.ensureTurn(taskID, depth)
	tl.markTurnActivity(ot)
	if ot.think == nil {
		ot.think = &thinkingItem{depth: depth, nested: true}
		tl.pushItem(ot.think)
	}
	ot.think.text += delta
	ot.think.bump()
	tl.invalidateItem(ot.think)
	if ot.resp.content == "" {
		ot.resp.status = "thinking…"
		ot.resp.bump()
		tl.invalidateItem(ot.resp)
	}
}

func (tl *timeline) endTurn(taskID string) {
	if sa := tl.subAgents[taskID]; sa != nil {
		sa.endTurn()
		return
	}
	ot := tl.turns[taskID]
	if ot == nil {
		return
	}
	if ot.think != nil {
		ot.think.done = true
		ot.think.bump()
		tl.invalidateItem(ot.think)
	}
	ot.resp.done = true
	ot.resp.bump()
	tl.invalidateItem(ot.resp)
	if ot.skills != nil {
		ot.skills.closed = true
		ot.skills.bump()
		tl.invalidateItem(ot.skills)
	}
	delete(tl.turns, taskID)
}

func (tl *timeline) startToolWithParent(taskID string, depth int, toolID, name, arg, parentToolID string, sequence int) {
	if parentToolID != "" {
		child := &toolItem{depth: depth + 1, name: name, arg: arg, state: toolRunning, sequence: sequence}
		if ref, ok := tl.toolIdx[parentToolID]; ok && ref.tool != nil {
			tl.attachChildTool(ref, toolID, child, sequence)
			return
		}
		if tl.pendingChildren == nil {
			tl.pendingChildren = make(map[string][]pendingToolChild)
		}
		tl.pendingChildren[parentToolID] = append(tl.pendingChildren[parentToolID], pendingToolChild{toolID: toolID, sequence: sequence, tool: child})
		tl.toolIdx[toolID] = toolRef{tool: child}
		return
	}
	if sa := tl.subAgents[taskID]; sa != nil {
		sa.startTool(toolID, name, arg)
		return
	}
	if ot := tl.turns[taskID]; ot != nil && ot.resp.content == "" {
		ot.resp.status = "running " + name
		ot.resp.bump()
		tl.invalidateItem(ot.resp)
	}
	if ot := tl.turns[taskID]; ot != nil {
		tl.markTurnActivity(ot)
	}
	g := tl.ensureToolGroup(depth)
	// Collapsed by default — the transcript stays a scannable list of one-line
	// tool rows; the per-row [+] expands a result on demand. group handles indent.
	t := &toolItem{name: name, arg: arg, state: toolRunning, notify: g.bump}
	g.add(t)
	tl.toolIdx[toolID] = toolRef{group: g, tool: t}
	tl.attachPendingChildren(toolID, toolRef{group: g, tool: t})
}

func (tl *timeline) attachChildTool(parentRef toolRef, toolID string, child *toolItem, sequence int) {
	child.notify = notifyParent(parentRef.tool.bump, parentRef.tool.notify)
	insertChildBySequence(parentRef.tool, child, sequence)
	tl.toolIdx[toolID] = toolRef{group: parentRef.group, tool: child}
	tl.bumpToolOwner(parentRef)
}

func (tl *timeline) attachPendingChildren(parentToolID string, parentRef toolRef) {
	pending := tl.pendingChildren[parentToolID]
	if len(pending) == 0 || parentRef.tool == nil {
		return
	}
	delete(tl.pendingChildren, parentToolID)
	for _, p := range pending {
		tl.attachChildTool(parentRef, p.toolID, p.tool, p.sequence)
	}
}

func (tl *timeline) bumpToolOwner(ref toolRef) {
	if ref.tool != nil {
		ref.tool.bump()
		return
	}
	if ref.group != nil {
		ref.group.bump()
	}
}

func insertChildBySequence(parent, child *toolItem, sequence int) {
	if parent == nil {
		return
	}
	child.sequence = sequence
	idx := len(parent.children)
	for i, existing := range parent.children {
		if existing != nil && existing.sequence > sequence {
			idx = i
			break
		}
	}
	parent.children = append(parent.children, nil)
	copy(parent.children[idx+1:], parent.children[idx:])
	parent.children[idx] = child
}

func (tl *timeline) finishTool(toolID, result string, data any, dur time.Duration, failed bool, failKind tools.Kind, effects ...string) {
	ref, ok := tl.toolIdx[toolID]
	if ok {
		ref.tool.state = toolOK
		if failed {
			ref.tool.state = toolFailed
		}
		ref.tool.waiting = false
		ref.tool.failKind = failKind
		ref.tool.result = result
		ref.tool.data = data
		ref.tool.effect = firstEffectSummary(effects)
		ref.tool.dur = dur
		tl.bumpToolOwner(ref)
		return
	}
	// Check sub-agent tool indices — tools spawned by sub-agents are
	// registered in the sub-agent's own index.
	for _, sa := range tl.subAgents {
		if ref, ok := sa.toolIdx[toolID]; ok {
			ref.tool.state = toolOK
			if failed {
				ref.tool.state = toolFailed
			}
			ref.tool.waiting = false
			ref.tool.failKind = failKind
			ref.tool.result = result
			ref.tool.data = data
			ref.tool.effect = firstEffectSummary(effects)
			ref.tool.dur = dur
			ref.group.bump()
			sa.bump()
			return
		}
	}
}

func (tl *timeline) waitTool(toolID string, access tools.WorkspaceAccess, paths []string) {
	if ref, ok := tl.toolRef(toolID); ok {
		ref.tool.waiting = true
		ref.tool.waitAccess = access
		ref.tool.waitPaths = append(ref.tool.waitPaths[:0], paths...)
		ref.tool.waitDuration = 0
		tl.bumpToolOwner(ref)
	}
}

func (tl *timeline) resumeTool(toolID string, duration time.Duration) {
	if ref, ok := tl.toolRef(toolID); ok {
		ref.tool.waiting = false
		ref.tool.waitDuration = duration
		tl.bumpToolOwner(ref)
	}
}

func (tl *timeline) toolRef(toolID string) (toolRef, bool) {
	if ref, ok := tl.toolIdx[toolID]; ok {
		return ref, true
	}
	for _, sa := range tl.subAgents {
		if ref, ok := sa.toolIdx[toolID]; ok {
			return ref, true
		}
	}
	return toolRef{}, false
}

func workspaceWaitSummary(access tools.WorkspaceAccess, paths []string) string {
	scope := "workspace"
	switch len(paths) {
	case 1:
		if paths[0] != "." {
			scope = paths[0]
		}
	case 0:
	default:
		scope = fmt.Sprintf("%d paths", len(paths))
	}
	return access.String() + " · " + scope
}

func firstEffectSummary(effects []string) string {
	if len(effects) == 0 {
		return ""
	}
	return effects[0]
}

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
