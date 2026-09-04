package tui

import (
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
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
	thread          *transcript.Reducer
	pendingPersist  transcript.Persistence
	items           []item
	toolIdx         map[string]toolRef            // ToolID -> its row + the group it lives in
	turns           map[string]*openTurn          // TaskID -> in-progress turn (split think/answer)
	cache           map[item]cacheEntry           // per-item render cache, keyed (width, version)
	pendingChildren map[string][]pendingToolChild // Parent ToolID -> children that arrived first
	queued          []*queuedUserItem
	queuedEntries   []string // FIFO user inputs waiting for SteerInjected

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
	tl.applyTranscript(transcript.Clear{})
	tl.clearItems()
	tl.toolIdx = make(map[string]toolRef)
	tl.pendingChildren = make(map[string][]pendingToolChild)
	tl.turns = make(map[string]*openTurn)
	tl.cache = make(map[item]cacheEntry)
	tl.queued = nil
	tl.queuedEntries = nil
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
		thread:           transcript.NewReducer(),
		toolIdx:          make(map[string]toolRef),
		pendingChildren:  make(map[string][]pendingToolChild),
		turns:            make(map[string]*openTurn),
		cache:            make(map[item]cacheEntry),
		subAgents:        make(map[string]*subAgentItem),
		subAgentsBySpawn: make(map[string]*subAgentItem),
		geometry:         timelineGeometry{dirty: 0},
	}
}

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
		if len(g.items) < len(tl.items) {
			// Extend instead of replacing these slices when they outgrow capacity.
			// Navigation depends on the measured prefix before dirty; replacing the
			// backing arrays zeroed that prefix, making scrollback see only the latest
			// incrementally rendered turn.
			grow := len(tl.items) - len(g.items)
			g.items = append(g.items, make([]item, grow)...)
			g.vers = append(g.vers, make([]uint64, grow)...)
			g.heights = append(g.heights, make([]int, grow)...)
			g.starts = append(g.starts, make([]int, grow)...)
			g.ends = append(g.ends, make([]int, grow)...)
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
	tl.applyTranscript(transcript.UserSubmitted{Text: text})
	tl.pushItem(&userItem{text: text})
}

func (tl *timeline) addQueuedUser(text string) {
	change := tl.applyTranscript(transcript.QueuedUserAdded{Text: text})
	timelineID := change.PrimaryEntryID
	q := &queuedUserItem{text: text}
	tl.appendItem(q)
	tl.queued = append(tl.queued, q)
	tl.queuedEntries = append(tl.queuedEntries, timelineID)
}

func (tl *timeline) addInjectedUser(text string) {
	if len(tl.queued) == 0 {
		tl.addUser(text)
		return
	}
	q := tl.queued[0]
	tl.queued = tl.queued[1:]
	entryID := tl.queuedEntries[0]
	tl.queuedEntries = tl.queuedEntries[1:]
	tl.applyTranscript(transcript.QueuedUserInjected{EntryID: entryID, Text: text})
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

func (tl *timeline) addNotice(text string) { tl.addNoticeForTurn("", text) }

func (tl *timeline) addNoticeForTurn(taskID, text string) {
	tl.applyTranscript(transcript.NoticeAdded{TurnID: taskID, Text: text})
	if sa := tl.subAgents[taskID]; sa != nil {
		sa.addNotice(text)
		return
	}
	tl.pushItem(&noticeItem{depth: 0, text: text})
}

// reserveSubAgent creates the visible transcript row as soon as agent_spawn
// starts. The child TaskID does not exist yet, so ConversationStarted later
// binds the reserved row through ParentToolCallID.
func (tl *timeline) reserveSubAgent(spawnToolID string, depth int, agentName, prompt string) *subAgentItem {
	tl.applyTranscript(transcript.SubagentReserved{SpawnToolID: spawnToolID, AgentName: agentName, Prompt: prompt})
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
	tl.applyTranscript(transcript.SubagentStarted{TurnID: taskID, SpawnToolID: spawnToolID, AgentName: agentName, Prompt: prompt})
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
	tl.applyTranscript(transcript.SubagentFinished{TurnID: taskID})
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
	tl.applyTranscript(transcript.SubagentSpawnFailed{SpawnToolID: spawnToolID, Detail: detail})
	if sa := tl.subAgentsBySpawn[spawnToolID]; sa != nil {
		sa.failLaunch(detail)
	}
}

// addLoadedSkill records a successfully loaded skill under the given turn.
// The skillsItem is always created at turn start; this just populates it.
func (tl *timeline) addLoadedSkill(taskID, name string) {
	tl.applyTranscript(transcript.SkillLoaded{TurnID: taskID, Name: name})
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
	tl.applyTranscript(transcript.TurnStarted{TurnID: taskID})
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
	tl.applyTranscript(transcript.AssistantDelta{TurnID: taskID, Delta: delta})
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
	tl.applyTranscript(transcript.ReasoningDelta{TurnID: taskID, Delta: delta})
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
	tl.applyTranscript(transcript.TurnFinished{TurnID: taskID})
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
	tl.applyTranscript(transcript.ToolStarted{TurnID: taskID, ToolID: toolID, ParentToolID: parentToolID, Name: name, Argument: arg, Sequence: sequence})
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
	tl.applyTranscript(transcript.ToolFinished{ToolID: toolID, Effect: firstEffectSummary(effects), FailureKind: failKind.String(), DurationMS: dur.Milliseconds(), Failed: failed})
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
