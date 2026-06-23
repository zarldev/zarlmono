package tui

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

const inspectorNavW = 24

type inspectorTab int

const (
	inspectorTabTools inspectorTab = iota
	inspectorTabPrompt
	inspectorTabGuardrails
	inspectorTabProcesses
	inspectorTabMCP
	inspectorTabEvents
	inspectorTabSkills
	inspectorTabAgents
	inspectorTabHooks
)

var inspectorTabNames = []string{"tools", "prompt", "guardrails", "processes", "mcp", "events", "skills", "agents", "hooks"}

// InspectorSnapshot holds a read-only view of the runner's current state, built
// on demand without starting a run or mutating persistent registries.
type InspectorSnapshot struct {
	// Tools is the tool roster that would be registered for the next turn.
	Tools []tools.ToolSpec
	// PlanMode is whether the runner is in PLAN mode.
	PlanMode bool
	// PromptSystem is the rendered system prompt for the current mode.
	PromptSystem string
	// PromptStack records neutral prompt-fragment accounting for inspection.
	PromptStack prompts.Stack
	// Errors are non-fatal snapshot/render issues surfaced in the inspector.
	Errors []string
	// Guardrails is a summary of guardrail configuration.
	Guardrails string
	// Processes lists background bash processes tracked by the live ProcessManager.
	Processes []code.ProcessInfo
	// MCPServers lists configured/running MCP servers (names only, redacted).
	MCPServers []string
	// EventLog contains recent runner events from the session.
	EventLog []EventRingEntry
	// Skills is the loaded skill catalog for the workspace.
	Skills []catalog.Skill
	// Agents is the loaded agent catalog for the workspace.
	Agents []catalog.Agent
	// Hooks is the loaded command-hook catalog for the workspace — what the
	// next turn's hook guardrail arms.
	Hooks []catalog.Hook
}

type inspector struct {
	snapshot      InspectorSnapshot
	cursor        int // sidebar cursor; index into inspectorTabNames
	processCursor int
	status        string
	scroll        int
	height        int
}

func newInspector(snapshot InspectorSnapshot) *inspector {
	return &inspector{snapshot: snapshot}
}

func (d *inspector) fullScreen() bool { return true }

func (d *inspector) handleKey(msg tea.KeyPressMsg) action {
	if inspectorTab(d.cursor) == inspectorTabProcesses {
		switch msg.String() {
		case "up", "k":
			if d.processCursor > 0 {
				d.processCursor--
			}
			return actionNone{}
		case "down", "j":
			if d.processCursor < len(d.snapshot.Processes)-1 {
				d.processCursor++
			}
			return actionNone{}
		case "x", "delete", "backspace":
			p, ok := d.selectedProcess()
			if !ok {
				d.status = "no process selected"
				return actionNone{}
			}
			if !p.Running {
				d.status = "process already exited"
				return actionNone{}
			}
			d.status = "killing " + p.ID + "…"
			return actionKillProcess{processID: p.ID, signal: "TERM"}
		}
	}
	switch msg.String() {
	case "esc", "ctrl+o", "q":
		return actionClose{}
	case "tab":
		d.cursor = (d.cursor + 1) % len(inspectorTabNames)
		d.scroll = 0
		return actionNone{}
	case "shift+tab", "left":
		d.cursor = (d.cursor - 1 + len(inspectorTabNames)) % len(inspectorTabNames)
		d.scroll = 0
		return actionNone{}
	case "right":
		d.cursor = (d.cursor + 1) % len(inspectorTabNames)
		d.scroll = 0
		return actionNone{}
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
			d.scroll = 0
		}
		return actionNone{}
	case "down", "j":
		if d.cursor < len(inspectorTabNames)-1 {
			d.cursor++
			d.scroll = 0
		}
		return actionNone{}
	case "pgup":
		d.scroll -= max(1, d.height-4)
		if d.scroll < 0 {
			d.scroll = 0
		}
	case "pgdown":
		d.scroll += max(1, d.height-4)
	case "home", "g":
		d.scroll = 0
	}
	return actionNone{}
}

func (d *inspector) selectedProcess() (code.ProcessInfo, bool) {
	if d == nil || len(d.snapshot.Processes) == 0 {
		return code.ProcessInfo{}, false
	}
	if d.processCursor < 0 {
		d.processCursor = 0
	}
	if d.processCursor >= len(d.snapshot.Processes) {
		d.processCursor = len(d.snapshot.Processes) - 1
	}
	return d.snapshot.Processes[d.processCursor], true
}

func (d *inspector) draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() < 50 || area.Dy() < 8 {
		return
	}
	l, ok := drawSplitPane(scr, area, "inspector", inspectorNavW)
	if !ok {
		return
	}
	d.height = l.Detail.Dy()
	drawPaneRow(scr, l.Context, palette.Muted.On(" "+d.tabBar()), palette.Subtle.On("ctrl+o close "))
	drawPaneHRule(scr, l.Context.Min.X, l.Context.Min.Y+1, l.Context.Dx(), -1, "")

	// Nav: tab names
	for i, name := range inspectorTabNames {
		screenY := l.Nav.Min.Y + i
		if screenY >= l.Nav.Max.Y {
			break
		}
		drawListRow(scr, uv.Rect(l.Nav.Min.X, screenY, l.Nav.Dx(), 1), name, i == d.cursor, true)
	}

	cw := l.Detail.Dx() - scrollbarWidth // reserve the gutter
	contentLines := d.contentLines(cw)
	d.scroll = clampScrollOffset(d.scroll, len(contentLines), l.Detail.Dy())
	for i := d.scroll; i < len(contentLines) && i-d.scroll < l.Detail.Dy(); i++ {
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+i-d.scroll, cw, 1),
			ansi.Truncate(contentLines[i], cw, ""))
	}
	drawPaneScrollbar(scr, l.Detail.Max.X-1, l.Detail.Min.Y, l.Detail.Dy(), len(contentLines), d.scroll)
	d.drawFooter(scr, l.Footer)
}

// scrollLines scrolls the detail pane by n lines (negative = up); the upper
// bound is clamped in draw against the live content height. Satisfies scroller.
func (d *inspector) scrollLines(n int) {
	d.scroll += n
	if d.scroll < 0 {
		d.scroll = 0
	}
}

func (d *inspector) tabBar() string {
	parts := make([]string, len(inspectorTabNames))
	for i, name := range inspectorTabNames {
		if i == d.cursor {
			parts[i] = palette.Primary.On("[ " + name + " ]")
		} else {
			parts[i] = palette.Subtle.On(name)
		}
	}
	return strings.Join(parts, "  ")
}

func (d *inspector) contentLines(width int) []string {
	switch inspectorTab(d.cursor) {
	case inspectorTabPrompt:
		return d.promptLines(width)
	case inspectorTabGuardrails:
		return d.guardrailsLines()
	case inspectorTabProcesses:
		return d.processLines(width)
	case inspectorTabMCP:
		return d.mcpLines()
	case inspectorTabEvents:
		return d.eventLines()
	case inspectorTabSkills:
		return d.skillsLines(width)
	case inspectorTabAgents:
		return d.agentsLines(width)
	case inspectorTabHooks:
		return d.hooksLines(width)
	default:
		return d.toolLines(width)
	}
}

func (d *inspector) toolLines(width int) []string {
	specs := d.snapshot.Tools
	if len(specs) == 0 {
		return append(d.errorLines(), palette.Muted.On(" no tools registered"))
	}
	slices.SortFunc(specs, func(a, b tools.ToolSpec) int { return cmp.Compare(a.Name, b.Name) })
	mode := "BUILD"
	if d.snapshot.PlanMode {
		mode = "PLAN"
	}
	total := 0
	for _, spec := range specs {
		if mode == "PLAN" && !engine.PlanAllows(spec.Name) {
			continue // blocked in PLAN mode — not part of this turn's surface
		}
		total++
	}
	lines := []string{
		headerLine(fmt.Sprintf("tools · %s mode · %d available", mode, total), width, palette.Primary.On),
		"",
	}
	lines = append(lines, d.errorLines()...)
	if len(d.snapshot.Errors) > 0 {
		lines = append(lines, "")
	}
	for _, spec := range specs {
		visible := true
		if mode == "PLAN" && !engine.PlanAllows(spec.Name) {
			visible = false
		}
		marker := ""
		if !visible {
			marker = palette.Muted.On(" (hidden)")
		}
		lines = append(lines, fmt.Sprintf("  %s%s", palette.Info.On(string(spec.Name)), marker))
		lines = append(lines, fmt.Sprintf("    %s", palette.Muted.On(spec.Description)))
	}
	return lines
}

// guardrailSummary renders the active guardrail configuration for the inspector
// from the real Deps, so it reflects what source() actually wires in rather than
// a hand-maintained string that silently drifts when a limit changes.
func guardrailSummary(deps guardrails.Deps) string {
	var b strings.Builder
	for _, v := range deps.Verifiers {
		if exts := strings.Join(v.Extensions(), ","); exts != "" {
			fmt.Fprintf(&b, "verifier: %s (%s)\n", v.Name(), exts)
		} else {
			fmt.Fprintf(&b, "verifier: %s\n", v.Name())
		}
	}
	if len(deps.FanoutLimits) > 0 {
		names := make([]tools.ToolName, 0, len(deps.FanoutLimits))
		for n := range deps.FanoutLimits {
			names = append(names, n)
		}
		slices.SortFunc(names, cmp.Compare)
		b.WriteString("fanout:")
		for _, n := range names {
			fmt.Fprintf(&b, " %s≤%d", n, deps.FanoutLimits[n])
		}
		b.WriteString("\n")
	}
	if deps.TestEdit != nil {
		fmt.Fprintf(&b, "test_edit: %s\n", deps.TestEdit.Name())
	}
	if deps.DecomposeJudge != nil {
		b.WriteString("decompose_judge: llm (constrained verdicts)\n")
	}
	if b.Len() == 0 {
		return "(none configured)"
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *inspector) promptLines(width int) []string {
	text := d.snapshot.PromptSystem
	if text == "" {
		return append(d.errorLines(), palette.Muted.On(" prompt not available — live runner not available"))
	}
	lines := []string{
		headerLine("system prompt · "+modeLabel(d.snapshot.PlanMode), width, palette.Primary.On),
		"",
	}
	lines = append(lines, promptStackSummaryLines(d.snapshot.PromptStack)...)
	if len(d.snapshot.PromptStack.Fragments) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, d.errorLines()...)
	if len(d.snapshot.Errors) > 0 {
		lines = append(lines, "")
	}
	for _, ln := range strings.Split(text, "\n") {
		lines = append(lines, palette.Muted.On(ln))
	}
	return lines
}

func promptStackSummaryLines(stack prompts.Stack) []string {
	if len(stack.Fragments) == 0 {
		return nil
	}
	lines := []string{
		palette.Subtle.On(fmt.Sprintf("prompt stack: %d fragments · %d words · %d bytes", len(stack.Fragments), stack.TotalWords, stack.TotalBytes)),
	}
	largest := make([]prompts.Fragment, 0, len(stack.Fragments))
	for _, f := range stack.Fragments {
		if f.Kind == prompts.FragmentRenderedTotal {
			continue
		}
		largest = append(largest, f)
		source := f.Source
		if source == "" {
			source = "(unknown source)"
		}
		lines = append(lines, fmt.Sprintf("  %02d %-21s %s · %d words · %d bytes", f.Order, f.Kind, palette.Info.On(f.Name), f.Words, f.Bytes))
		lines = append(lines, "     "+palette.Subtle.On(source))
	}
	slices.SortFunc(largest, func(a, b prompts.Fragment) int { return cmp.Compare(b.Bytes, a.Bytes) })
	if len(largest) > 0 {
		lines = append(lines, palette.Subtle.On("largest fragments"))
		for i, f := range largest {
			if i >= 3 {
				break
			}
			lines = append(lines, fmt.Sprintf("  %s · %s · %d bytes", palette.Info.On(f.Name), f.Kind, f.Bytes))
		}
	}
	return lines
}

func promptStackFragment(stack prompts.Stack, kind prompts.FragmentKind, name string) (prompts.Fragment, bool) {
	for _, f := range stack.Fragments {
		if f.Kind == kind && f.Name == name {
			return f, true
		}
	}
	return prompts.Fragment{}, false
}

func contributionLabel(contributes bool) string {
	if contributes {
		return "contributes now"
	}
	return "catalogued; loaded on demand"
}

func (d *inspector) errorLines() []string {
	if len(d.snapshot.Errors) == 0 {
		return nil
	}
	lines := []string{palette.Warning.On("snapshot warnings")}
	for _, err := range d.snapshot.Errors {
		lines = append(lines, "  "+palette.Muted.On(err))
	}
	return lines
}

func modeLabel(plan bool) string {
	if plan {
		return "PLAN mode"
	}
	return "BUILD mode"
}

func (d *inspector) guardrailsLines() []string {
	text := d.snapshot.Guardrails
	if text == "" {
		return []string{palette.Muted.On(" guardrails not configured")}
	}
	lines := []string{
		headerLine("guardrails", 80, palette.Primary.On),
		"",
		palette.Muted.On(text),
	}
	// Command hooks ride the same chain (appended after the production set);
	// summarise the armed counts here, details on the hooks tab.
	if pre, post := hookEventCounts(d.snapshot.Hooks); pre+post > 0 {
		lines = append(lines, palette.Muted.On(fmt.Sprintf("hooks: %d pre_tool, %d post_tool", pre, post)))
	}
	return append(lines, "")
}

// hookEventCounts splits the hook catalog into pre/post counts for the
// guardrail summary line.
func hookEventCounts(hooks []catalog.Hook) (int, int) {
	var pre, post int
	for _, h := range hooks {
		switch h.Event {
		case catalog.HookPreTool:
			pre++
		case catalog.HookPostTool:
			post++
		}
	}
	return pre, post
}

func (d *inspector) processLines(width int) []string {
	procs := d.snapshot.Processes
	if len(procs) == 0 {
		return []string{palette.Muted.On(" no background processes tracked")}
	}
	running := 0
	for _, p := range procs {
		if p.Running {
			running++
		}
	}
	lines := []string{
		headerLine(fmt.Sprintf("processes · %d running · %d tracked", running, len(procs)), width, palette.Primary.On),
		"",
	}
	for _, p := range procs {
		selected := d.processCursor >= 0 && d.processCursor < len(procs) && procs[d.processCursor].ID == p.ID
		state := palette.Success.On("running")
		if !p.Running {
			state = palette.Muted.On(fmt.Sprintf("exited %d", p.ExitCode))
		}
		age := time.Since(p.StartedAt).Round(time.Second)
		marker := " "
		if selected {
			marker = palette.Primary.On("▶")
		}
		lines = append(lines, fmt.Sprintf("%s %s  pid=%d  %s  age=%s", marker, palette.Info.On(p.ID), p.PID, state, age))
		lines = append(lines, fmt.Sprintf("    %s", palette.Muted.On(p.Command)))
		lines = append(lines, fmt.Sprintf("    cwd=%s · stdout=%d lines · stderr=%d lines", palette.Muted.On(p.CWD), p.StdoutLines, p.StderrLines))
	}
	if d.status != "" {
		lines = append(lines, "", palette.Muted.On(d.status))
	}
	return lines
}

func (d *inspector) mcpLines() []string {
	servers := d.snapshot.MCPServers
	if len(servers) == 0 {
		return []string{palette.Muted.On(" no MCP servers configured")}
	}
	lines := []string{
		headerLine("mcp servers", 80, palette.Primary.On),
		"",
	}
	for _, s := range servers {
		lines = append(lines, palette.Info.On("  connected: ")+palette.Muted.On(s))
	}
	return lines
}

func (d *inspector) eventLines() []string {
	events := d.snapshot.EventLog
	if len(events) == 0 {
		return []string{palette.Muted.On(" no events recorded yet")}
	}
	lines := []string{
		headerLine(fmt.Sprintf("events · %d recorded", len(events)), 80, palette.Primary.On),
		"",
	}
	for _, e := range events {
		ts := e.At.Format("15:04:05")
		lines = append(lines, fmt.Sprintf("  %s  %s  %s",
			palette.Subtle.On(ts),
			palette.Primary.On(e.Kind),
			palette.Muted.On(e.Detail),
		))
	}
	return lines
}

func (d *inspector) skillsLines(width int) []string {
	skills := d.snapshot.Skills
	if len(skills) == 0 {
		return []string{palette.Muted.On(" no skills loaded")}
	}
	lines := []string{
		headerLine(fmt.Sprintf("skills · %d loaded", len(skills)), width, palette.PlanMode.On),
		"",
	}
	for _, s := range skills {
		lines = append(lines, fmt.Sprintf("  %s %s", palette.PlanMode.On("#"), palette.Info.On(s.Name)))
		lines = append(lines, fmt.Sprintf("    %s", palette.Muted.On(s.Description)))
		lines = append(lines, fmt.Sprintf("    %s", palette.Subtle.On(s.Source)))
		if f, ok := promptStackFragment(d.snapshot.PromptStack, prompts.FragmentSkill, s.Name); ok {
			lines = append(lines, fmt.Sprintf("    %s", palette.Subtle.On(fmt.Sprintf("prompt fragment: %d words · %d bytes · %s", f.Words, f.Bytes, contributionLabel(f.Contributes)))))
		}
		lines = append(lines, "")
	}
	return lines
}

func (d *inspector) agentsLines(width int) []string {
	agents := d.snapshot.Agents
	if len(agents) == 0 {
		return []string{palette.Muted.On(" no agents loaded")}
	}
	lines := []string{
		headerLine(fmt.Sprintf("agents · %d loaded", len(agents)), width, palette.Info.On),
		"",
	}
	for _, a := range agents {
		lines = append(lines, fmt.Sprintf("  %s %s", palette.Info.On("@"), palette.Primary.On(a.Name)))
		lines = append(lines, fmt.Sprintf("    %s", palette.Muted.On(a.Description)))
		if a.Provider != "" || a.Model != "" {
			lines = append(lines, fmt.Sprintf("    provider=%s model=%s", palette.Subtle.On(a.Provider), palette.Subtle.On(a.Model)))
		}
		lines = append(lines, fmt.Sprintf("    %s", palette.Subtle.On(a.Source)))
		lines = append(lines, "")
	}
	return lines
}

func (d *inspector) hooksLines(width int) []string {
	hooks := d.snapshot.Hooks
	if len(hooks) == 0 {
		return []string{palette.Muted.On(" no hooks loaded")}
	}
	lines := []string{
		headerLine(fmt.Sprintf("hooks · %d armed", len(hooks)), width, palette.Warning.On),
		"",
	}
	for _, h := range hooks {
		lines = append(lines, fmt.Sprintf("  %s %s", palette.Warning.On("!"), palette.Primary.On(h.Name)))
		lines = append(lines, fmt.Sprintf("    %s", palette.Muted.On(h.Description)))
		trigger := string(h.Event)
		if h.Matcher != "" {
			trigger += " · matcher=" + h.Matcher
		}
		if h.Blocking {
			trigger += " · blocking"
		}
		trigger += " · timeout=" + h.Timeout.String()
		lines = append(lines, fmt.Sprintf("    %s", palette.Subtle.On(trigger)))
		lines = append(lines, fmt.Sprintf("    %s", palette.Subtle.On(h.Source)))
		lines = append(lines, "")
	}
	return lines
}

func (d *inspector) drawFooter(scr uv.Screen, r uv.Rectangle) {
	hints := []keyHint{{"↑↓/jk", "navigate"}, {"tab/←→", "switch tab"}, {"pgup/pgdn", "page"}}
	if inspectorTab(d.cursor) == inspectorTabProcesses {
		hints = []keyHint{{"↑↓/jk", "select process"}, {"x", "kill"}, {"tab/←→", "switch tab"}, {"pgup/pgdn", "page"}}
	}
	hints = append(hints, keyHint{"esc", "close"})
	footer := keyLegend(hints...)
	drawPaneRow(scr, r, palette.Subtle.On(" "+footer), "")
}

// BuildInspectorSnapshot builds a read-only snapshot of the runner's state for
// the inspector overlay. It does not start a run or mutate persistent state.
func BuildInspectorSnapshot(session *Session, live *engine.LiveRunner, catalog *engine.RuntimeCatalog) InspectorSnapshot {
	s := InspectorSnapshot{}
	ctx := context.Background()
	if session != nil && session.EventLog != nil {
		s.EventLog = session.EventLog.Snapshot()
	}
	if live != nil {
		ins := live.Inspect(ctx)
		s.PlanMode = ins.PlanMode
		s.Processes = ins.Processes
		s.Errors = ins.Errors
		s.Tools = ins.Tools
		s.Guardrails = guardrailSummary(ins.Guardrails)
		s.PromptStack = ins.PromptStack
		s.Skills = ins.Skills
		s.Agents = ins.Agents
		s.Hooks = ins.Hooks
		if ins.MCPActive {
			s.MCPServers = append(s.MCPServers, "MCP registry active")
		}
		if ins.PromptSystem != "" {
			s.PromptSystem = inspectorPromptHeader(ins.PlanMode, ins.WorkspaceRoot, ins.Model) + ins.PromptSystem
		}
		return s
	}

	// Build a minimal tool registry snapshot when the live runner is unavailable:
	// standard tools plus catalog tools. Production Ctrl-O passes a live runner;
	// this fallback keeps tests / early startup useful without pretending to have a
	// rendered runner prompt.
	reg := tools.NewRegistry()
	if catalog != nil {
		reg.Register(engine.NewListSkillsTool(catalog))
		reg.Register(engine.NewLoadSkillTool(catalog))
		reg.Register(engine.NewListAgentsTool(catalog))
	}
	for t := range reg.Tools(ctx) {
		s.Tools = append(s.Tools, t.Definition())
	}
	s.PromptSystem = "live runner not available — start a run first"
	return s
}

func inspectorPromptHeader(plan bool, workspace, model string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "[inspector] rendered next-turn %s prompt\n", modeLabel(plan))
	if workspace != "" {
		fmt.Fprintf(&b, "workspace: %s\n", workspace)
	}
	if model != "" {
		fmt.Fprintf(&b, "model: %s\n", model)
	}
	b.WriteString("---\n")
	return b.String()
}
