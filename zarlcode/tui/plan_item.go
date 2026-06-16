package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// planItem renders an update_plan call inline in the transcript. It mirrors the
// other activity rows (thinking, tools, edits): a collapsed [+] summary by
// default, expandable with browse+enter or mouse click to inspect the snapshot.
type planItem struct {
	versioned
	depth    int
	nested   bool
	plan     code.Plan
	expanded bool
}

func (p *planItem) finished() bool { return true }

func (p *planItem) toggle() {
	p.expanded = !p.expanded
	p.bump()
}

func (p *planItem) togglerAt(_, ln int) toggler {
	if ln == 0 {
		return p
	}
	return nil
}

func (p *planItem) render(width int) []string {
	glyph := "[+]"
	if p.expanded {
		glyph = "[-]"
	}
	lines := []string{palette.Subtle.On(glyph+" ") + palette.PlanMode.On(planNotice(p.plan))}
	if p.expanded {
		lines = append(lines, renderContentBlock(width, contentBlock{kind: contentPlan, plan: p.plan, depth: p.depth})...)
	}
	if p.nested {
		lines = prefixLines(lines, nestPad)
	}
	return indentLines(lines, p.depth)
}

func planInlineLines(p code.Plan, width int) []string {
	if p.IsEmpty() {
		return []string{"  " + palette.Muted.On("no structured plan currently")}
	}
	done, doing, pending := planCounts(p)
	out := []string{
		"  " + palette.Muted.On(fmt.Sprintf(
			"%d steps · %d done · %d in progress · %d pending", len(p.Steps), done, doing, pending)),
	}
	if p.Explanation != "" {
		out = append(out, renderContentBlock(width, contentBlock{
			kind:       contentPlain,
			text:       "latest update: " + p.Explanation,
			bodyPrefix: "  ",
			style:      palette.Subtle.On,
		})...)
	}
	numW := len(strconv.Itoa(len(p.Steps)))
	for i, s := range p.Steps {
		glyph, style := planStepDecor(s.Status)
		prefix := fmt.Sprintf("  %*d. %s ", numW, i+1, glyph)
		out = append(out, renderPlain(width, s.Text,
			withFirstPrefix(prefix, strings.Repeat(" ", ansi.StringWidth(prefix))),
			withStyle(style),
		)...)
	}
	return out
}

func (tl *timeline) addPlanUpdate(p code.Plan) {
	// update_plan sends the full current plan every time. Keep repeated updates
	// within the current turn on a single live transcript row instead of appending
	// a new "plan updated" line for every step transition; the plan pane remains
	// the complete source of truth, and the timeline stays scannable.
	for i := len(tl.items) - 1; i >= 0; i-- {
		if it, ok := tl.items[i].(*planItem); ok {
			it.plan = p
			it.nested = len(tl.turns) > 0
			it.bump()
			return
		}
		if !itemNested(tl.items[i]) {
			break
		}
	}
	tl.items = append(tl.items, &planItem{plan: p, nested: len(tl.turns) > 0})
}
