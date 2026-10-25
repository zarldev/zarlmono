package tui

import (
	"fmt"
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// syncLivePlan retains the inspected step across progress updates, but starts
// replaced plans at current work rather than carrying an unrelated row offset.
func (d *planDialog) syncLivePlan() {
	if slices.EqualFunc(d.liveSteps, d.plan.Steps, func(a, b code.PlanStep) bool { return a.Text == b.Text }) {
		return
	}
	d.liveSteps = slices.Clone(d.plan.Steps)
	d.liveCursor, d.liveScroll = 0, 0
	d.selectCurrentStep()
}

func (d *planDialog) selectLiveStep(index int) {
	index = min(max(0, index), max(0, len(d.plan.Steps)-1))
	if index != d.liveCursor {
		d.liveCursor, d.liveScroll = index, 0
	}
}

func (d *planDialog) selectCurrentStep() {
	for _, status := range []code.StepStatus{code.StepStatuses.INPROGRESS, code.StepStatuses.PENDING} {
		for i, step := range d.plan.Steps {
			if step.Status == status {
				d.selectLiveStep(i)
				return
			}
		}
	}
}

func (d *planDialog) selectUnfinishedStep(direction int) {
	n := len(d.plan.Steps)
	for offset := 1; offset <= n; offset++ {
		index := (d.liveCursor + direction*offset + n) % n
		if d.plan.Steps[index].Status != code.StepStatuses.COMPLETED {
			d.selectLiveStep(index)
			return
		}
	}
}

func (d *planDialog) handleLiveKey(key string) {
	d.syncLivePlan()
	switch key {
	case "up", "k":
		d.selectLiveStep(d.liveCursor - 1)
	case "down", "j":
		d.selectLiveStep(d.liveCursor + 1)
	case "home", "g":
		d.selectLiveStep(0)
		d.liveScroll = 0
	case "end", "G":
		d.selectLiveStep(len(d.plan.Steps) - 1)
	case "a":
		d.selectCurrentStep()
		d.liveScroll = 0
	case "n":
		d.selectUnfinishedStep(1)
	case "p":
		d.selectUnfinishedStep(-1)
	case "pgup":
		d.scrollLines(-max(1, d.liveViewport-1))
	case "pgdown":
		d.scrollLines(max(1, d.liveViewport-1))
	}
}

// scrollLines lets the overlay own wheel scrolling without moving the sidebar
// or changing the inspected step. Saved previews retain the same wheel contract.
func (d *planDialog) scrollLines(delta int) {
	if d.view == planViewSaved {
		d.scroll = max(0, d.scroll+delta)
		return
	}
	d.syncLivePlan()
	d.liveScroll = clampScrollOffset(d.liveScroll+delta, len(d.liveDetailLines()), max(1, d.liveViewport))
}

func (d *planDialog) drawLive(scr uv.Screen, area uv.Rectangle) {
	d.syncLivePlan()
	l := drawDetailView(scr, area, "plan", []string{"live", "saved"}, int(d.view))
	if l.Body.Empty() {
		return
	}
	if d.plan.IsEmpty() {
		lines := renderPlain(l.Body.Dx(), "no structured plan yet\nswitch to plan with shift+tab, then ask for a plan", withStyle(palette.Muted.On))
		for i, line := range lines[:min(len(lines), l.Body.Dy())] {
			drawLine(scr, uv.Rect(l.Body.Min.X, l.Body.Min.Y+i, l.Body.Dx(), 1), line)
		}
		drawDetailFooter(scr, l.Footer, "esc close · tab saved plans", "")
		return
	}
	body := d.drawLiveProgress(scr, l.Body)
	nav, detail := body, body
	switch {
	case body.Dx() >= 72:
		navW := min(44, body.Dx()/2)
		nav.Max.X = nav.Min.X + navW
		detail.Min.X = nav.Max.X + 3
		for y := body.Min.Y; y < body.Max.Y; y++ {
			drawLine(scr, uv.Rect(nav.Max.X+1, y, 1, 1), palette.Border.On("│"))
		}
	case body.Dy() >= 6:
		nav.Max.Y = nav.Min.Y + min(4, body.Dy()/3)
		detail.Min.Y = nav.Max.Y + 1
	default:
		nav.Max.Y = nav.Min.Y // Tiny screens keep the selected detail reachable.
	}
	d.drawLiveOutline(scr, nav)
	d.drawLiveDetail(scr, detail)
	hints := "↑↓ steps · tab saved plans · esc close"
	if l.Footer.Dx() >= 96 {
		hints = "↑↓ steps · a active · n/p unfinished · pgup/pgdn detail · tab saved plans · esc close"
	} else if l.Footer.Dx() >= 64 {
		hints = "↑↓ steps · a active · pgup/pgdn detail · tab saved plans · esc close"
	}
	drawDetailFooter(scr, l.Footer, hints, "read-only · updates live")
}

func (d *planDialog) drawLiveProgress(scr uv.Screen, body uv.Rectangle) uv.Rectangle {
	done, doing, pending := planCounts(*d.plan)
	title := sidebarHeading(fmt.Sprintf("current plan · %d steps", len(d.plan.Steps)))
	drawLine(scr, uv.Rect(body.Min.X, body.Min.Y, body.Dx(), 1), rowLayout(title, planProgressLine(done, len(d.plan.Steps), body.Dx()/2), body.Dx()))
	body.Min.Y++
	if body.Dy() > 2 {
		counts := fmt.Sprintf("%d done · %d in progress · %d pending", done, doing, pending)
		drawLine(scr, uv.Rect(body.Min.X, body.Min.Y, body.Dx(), 1), palette.Muted.On(counts))
		body.Min.Y += 2
	}
	return body
}

func (d *planDialog) drawLiveOutline(scr uv.Screen, r uv.Rectangle) {
	if r.Empty() {
		return
	}
	start, end := windowAroundCursor(d.liveCursor, len(d.plan.Steps), r.Dy())
	for i := start; i < end; i++ {
		step := d.plan.Steps[i]
		glyph, _ := planStepDecor(step.Status)
		label := fmt.Sprintf("%d. %s %s", i+1, glyph, strings.Join(strings.Fields(step.Text), " "))
		label = ansi.Truncate(label, max(1, r.Dx()-2), "…")
		style := palette.Muted.On
		if step.Status == code.StepStatuses.INPROGRESS || i == d.liveCursor {
			style = sidebarHeading
		}
		drawListRow(scr, uv.Rect(r.Min.X, r.Min.Y+i-start, r.Dx(), 1), style(label), i == d.liveCursor, true)
	}
}

func (d *planDialog) drawLiveDetail(scr uv.Screen, r uv.Rectangle) {
	if r.Empty() {
		return
	}
	step := d.plan.Steps[d.liveCursor]
	status := strings.ReplaceAll(step.Status.String(), "_", " ")
	heading := fmt.Sprintf("step %d of %d · %s", d.liveCursor+1, len(d.plan.Steps), status)
	drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), sidebarHeading(heading))
	r.Min.Y++
	d.liveWidth = max(1, min(planWrapWidth, r.Dx()-1))
	d.liveViewport = r.Dy()
	if r.Empty() {
		return
	}
	lines := d.liveDetailLines()
	d.liveScroll = clampScrollOffset(d.liveScroll, len(lines), d.liveViewport)
	for i, line := range lines[d.liveScroll:min(len(lines), d.liveScroll+d.liveViewport)] {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+i, d.liveWidth, 1), line)
	}
	drawPaneScrollbar(scr, r.Max.X-1, r.Min.Y, r.Dy(), len(lines), d.liveScroll)
}

func (d *planDialog) liveDetailLines() []string {
	if d.plan.IsEmpty() {
		return nil
	}
	lines := renderPlain(d.liveWidth, d.plan.Steps[d.liveCursor].Text, withStyle(palette.Fg.On))
	if d.plan.Explanation != "" {
		lines = append(lines, "", sidebarHeading("latest plan update"))
		lines = append(lines, renderPlain(d.liveWidth, d.plan.Explanation, withStyle(palette.Muted.On))...)
	}
	return lines
}
