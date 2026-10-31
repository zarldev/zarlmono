package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// sidebarSection keeps essential rows together. Overflow scrolls instead of
// dropping plan steps; spare space is used for secondary session details.
type sidebarSection struct {
	title   string
	detail  string
	rows    []string
	minimum int
}

// sidebarPlanCache owns its step snapshot and wrapped rows. Plan text, status,
// explanation, width and theme changes invalidate it; steady redraws do not wrap.
type sidebarPlanCache struct {
	plan    code.Plan
	width   int
	gen     uint64
	section sidebarSection
}

// resetSidebarForPlan preserves position for progress-only updates.
func (m *UI) resetSidebarForPlan(before code.Plan) {
	if !slices.EqualFunc(before.Steps, m.session.Plan.Steps, func(a, b code.PlanStep) bool { return a.Text == b.Text }) {
		m.sidebarScroll = 0
	}
}

func sidebarHeading(text string) string {
	return terminalChrome().PaneLabel(text, false)
}

func (s sidebarSection) heading() string {
	return sidebarHeading(s.title) + palette.Subtle.On(s.detail)
}

func (m *UI) stateSidebarContent(width, height int) []string {
	if height <= 0 {
		return nil
	}
	model, provider := m.session.Model, m.session.Provider
	if model == "" {
		model = "model not selected"
	}
	if provider == "" {
		provider = "provider not configured"
	}
	identity := []string{palette.Fg.On(model), palette.Muted.On(provider)}
	if effort := m.reasoningEffortLine(); effort != "" {
		identity[1] += palette.Muted.On(" · " + ansi.Strip(effort))
	}
	context := m.sidebarContext(width)
	plan := m.sidebarPlan(width)
	usage := m.sidebarUsage()
	sections := []sidebarSection{{rows: identity, minimum: 2}, context}
	if len(plan.rows) > 0 {
		sections = append(sections, plan)
	}
	sections = append(sections, usage)

	needed := len(sections) - 1 // one blank row between sections
	for _, section := range sections {
		needed += section.minimum
		if section.title != "" {
			needed++
		}
	}
	budget := max(0, height-needed)
	counts := make([]int, len(sections))
	for i, section := range sections {
		counts[i] = section.minimum
		extra := min(budget, len(section.rows)-section.minimum)
		counts[i] += extra
		budget -= extra
	}
	// Preserve secondary details before spending spare rows on breathing room.
	var workspace []string
	if budget >= 3 {
		workspace = m.sidebarWorkspace()
		if len(workspace) > 0 {
			workspace = workspace[:min(len(workspace), budget-2)]
			budget -= len(workspace) + 2
		}
	}
	out := make([]string, 0, height)
	for i, section := range sections {
		if i > 0 {
			out = append(out, "")
			// Give context and current work extra separation only when it fits.
			if budget > 0 && (i == 1 || (i == 2 && len(plan.rows) > 0)) {
				out = append(out, "")
				budget--
			}
		}
		if section.title != "" {
			out = append(out, section.heading())
		}
		out = append(out, section.rows[:counts[i]]...)
	}
	if len(workspace) > 0 {
		out = append(out, "", sidebarHeading("workspace"))
		out = append(out, workspace...)
	}
	return out
}

func (m *UI) sidebarContext(width int) sidebarSection {
	s := &m.session.Run
	section := sidebarSection{title: "context", minimum: 1}
	used := s.effectiveUsed()
	switch {
	case used == 0:
		section.rows = []string{palette.Muted.On("usage not reported")}
		if s.window > 0 {
			section.rows = append(section.rows, palette.Subtle.On(fmtCount(s.window)+" token window"))
		}
	case s.window <= 0:
		section.rows = []string{palette.Fg.On(fmtCount(used)) + palette.Muted.On(" used · window unknown")}
	default:
		section.rows = []string{palette.Fg.On(fmtCount(used)+" / "+fmtCount(s.window)) + palette.Muted.On(" · ") + pressureColor(s.fillFrac()).On(itoa(used*100/s.window)+"%")}
		if _, remaining, ok := s.compactionThreshold(); ok && remaining == 0 {
			section.rows = append(section.rows, palette.Warning.On("compaction due"))
			section.minimum++
		}
		filled := min(max(int(s.fillFrac()*float64(width)), 0), width)
		section.rows = append(section.rows, pressureColor(s.fillFrac()).On(strings.Repeat("━", filled))+palette.Border.On(strings.Repeat("─", width-filled)))
	}
	if threshold, remaining, ok := s.compactionThreshold(); ok && remaining > 0 && used > 0 {
		section.rows = append(section.rows, palette.Subtle.On("compact at "+fmtCount(threshold)))
	}
	return section
}

func (m *UI) sidebarPlan(width int) sidebarSection {
	plan := m.session.Plan
	if plan.IsEmpty() {
		m.sidebarPlanCache = sidebarPlanCache{}
		return sidebarSection{}
	}
	cache := &m.sidebarPlanCache
	if cache.width == width && cache.gen == themeGen && cache.plan.Explanation == plan.Explanation && slices.Equal(cache.plan.Steps, plan.Steps) {
		return cache.section
	}
	completed, _, _ := planCounts(plan)
	section := sidebarSection{title: "plan", detail: " · " + itoa(completed) + "/" + itoa(len(plan.Steps)) + " complete"}
	for _, step := range plan.Steps {
		glyph, _ := planStepDecor(step.Status)
		style := palette.Muted.On
		if step.Status == code.StepStatuses.INPROGRESS {
			style = func(text string) string { return palette.Fg.On("\x1b[1m" + text + "\x1b[22m") }
		}
		section.rows = append(section.rows, renderPlain(width, step.Text,
			withFirstPrefix(glyph+" ", "  "), withStyle(style))...)
	}
	if plan.Explanation != "" {
		section.rows = append(section.rows, "")
		section.rows = append(section.rows, renderPlain(width, "latest update: "+plan.Explanation,
			withStyle(palette.Subtle.On))...)
	}
	section.minimum = len(section.rows)
	*cache = sidebarPlanCache{
		plan:  code.Plan{Steps: slices.Clone(plan.Steps), Explanation: plan.Explanation},
		width: width, gen: themeGen, section: section,
	}
	return section
}

func (m *UI) sidebarUsage() sidebarSection {
	s := &m.session.Run
	section := sidebarSection{title: "usage", minimum: 1}
	switch {
	case s.hasPricing() || s.hasSessionCost():
		section.rows = []string{palette.Fg.On(fmtUSD(s.sessionCost())) + palette.Muted.On(" base-rate estimate")}
	case s.subscription:
		section.rows = []string{palette.Muted.On("subscription · unmetered")}
	case s.local:
		section.rows = []string{palette.Muted.On("local · unmetered")}
	default:
		section.rows = []string{palette.Muted.On("metered · rate unknown")}
	}
	if s.unpricedTasks > 0 || s.unpricedCacheWriteTasks > 0 {
		section.rows = append(section.rows, palette.Warning.On("cost incomplete · ctrl+l"))
		section.minimum++
	}
	if (s.hasPricing() || s.hasSessionCost()) && (s.subscription || s.local) {
		billing := "local · unmetered"
		if s.subscription {
			billing = "subscription · unmetered"
		}
		section.rows = append(section.rows, palette.Muted.On(billing))
		section.minimum++
	}
	if s.lastIn > 0 || s.lastOut > 0 {
		section.rows = append(section.rows, palette.Subtle.On("last ")+palette.Fg.On(fmtCount(s.lastIn)+" in · "+fmtCount(s.lastOut)+" out"))
	}
	if s.sessionToolCalls > 0 {
		section.rows = append(section.rows, palette.Subtle.On(itoa(s.sessionToolCalls)+" tool calls"))
	}
	return section
}

func (m *UI) sidebarWorkspace() []string {
	var rows []string
	if m.session.Workspace != "" {
		rows = append(rows, palette.Muted.On(m.session.Workspace))
	}
	if m.session.Branch != "" {
		rows = append(rows, palette.Muted.On(m.session.Branch))
	}
	if changes := m.workspaceChangesLine(); changes != "" {
		rows = append(rows, changes)
	}
	return rows
}
