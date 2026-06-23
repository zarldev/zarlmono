package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// ─── plan browser ───────────────────────────────────────────────────────

type planView int

const (
	planViewLive planView = iota
	planViewSaved
)

// planEntry is one saved plan file in .zarlcode/plans/.
type planEntry struct {
	name    string
	path    string
	modTime time.Time
}

// planDialog is the read-only plan overlay (ctrl+p). It has two tabs:
//
//	live  — the structured plan from update_plan (centered dialog)
//	saved — a full-screen split-pane browser for .zarlcode/plans/ files
//
// Tab switches between views; in saved view ↑↓ picks a plan and previews its
// markdown on the right. Tab returns to live.
type planDialog struct {
	plan      *code.Plan
	view      planView
	cursor    int
	entries   []planEntry
	workspace string

	// saved-tab preview state
	previewName    string    // path of the file currently previewed
	previewContent string    // cached markdown content
	previewModTime time.Time // modtime of the cached preview; same path may change on disk
	scroll         int       // preview scroll offset
	height         int
}

func newPlanDialog(plan *code.Plan, workspace string) *planDialog {
	d := &planDialog{plan: plan, workspace: workspace, view: planViewLive}
	d.loadEntries()
	return d
}

func (d *planDialog) planSummary() string {
	if d.plan == nil || d.plan.IsEmpty() {
		return "no active plan"
	}
	done, doing, pending := planCounts(*d.plan)
	return fmt.Sprintf("%d steps · %d done · %d active · %d pending", len(d.plan.Steps), done, doing, pending)
}

func (d *planDialog) savedSummary() string {
	if len(d.entries) == 0 {
		return "no saved plans"
	}
	if d.cursor >= 0 && d.cursor < len(d.entries) {
		entry := d.entries[d.cursor]
		return fmt.Sprintf("%d saved · %s", len(d.entries), entry.modTime.Format("2006-01-02 15:04"))
	}
	return fmt.Sprintf("%d saved", len(d.entries))
}

func (d *planDialog) loadEntries() {
	dir := filepath.Join(d.workspace, code.PlansDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	d.entries = d.entries[:0]
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		d.entries = append(d.entries, planEntry{
			name:    strings.TrimSuffix(e.Name(), ".md"),
			path:    filepath.Join(dir, e.Name()),
			modTime: info.ModTime(),
		})
	}
	slices.SortFunc(d.entries, func(a, b planEntry) int { return b.modTime.Compare(a.modTime) })
	if d.cursor >= len(d.entries) {
		d.cursor = max(0, len(d.entries)-1)
	}
}

func (d *planDialog) fullScreen() bool { return d.view == planViewSaved }

func (d *planDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "ctrl+p", "q":
		return actionClose{}
	case "tab":
		if d.view == planViewLive {
			d.view = planViewSaved
			d.loadEntries() // refresh the file list
			d.tryPreview()
		} else {
			d.view = planViewLive
		}
		return actionNone{}
	}

	if d.view == planViewSaved {
		switch msg.String() {
		case "up", "k":
			if d.cursor > 0 {
				d.cursor--
				d.tryPreview()
			}
		case "down", "j":
			if d.cursor < len(d.entries)-1 {
				d.cursor++
				d.tryPreview()
			}
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
	}
	return actionNone{}
}

func (d *planDialog) draw(scr uv.Screen, area uv.Rectangle) {
	switch d.view {
	case planViewLive:
		d.drawLive(scr, area)
	case planViewSaved:
		d.drawSaved(scr, area)
	}
}

// ─── live tab ───────────────────────────────────────────────────────────

func (d *planDialog) drawLive(scr uv.Screen, area uv.Rectangle) {
	var p code.Plan
	if d.plan != nil {
		p = *d.plan
	}
	wrap := planWrapForArea(area)
	lines := append([]string{
		overlayTopBar("plan", []string{"live", "saved"}, int(planViewLive), d.planSummary(), wrap),
	}, planLines(p, wrap)...)
	drawPlanDialogBox(scr, area, "planning pane", lines)
}

// ─── saved tab (full-screen split pane) ─────────────────────────────────

func (d *planDialog) tryPreview() {
	if d.cursor < 0 || d.cursor >= len(d.entries) {
		d.previewName = ""
		d.previewContent = ""
		d.previewModTime = time.Time{}
		d.scroll = 0
		return
	}
	e := d.entries[d.cursor]
	d.previewName = e.path
	d.previewModTime = e.modTime
	d.scroll = 0
	data, err := os.ReadFile(e.path)
	if err != nil {
		d.previewContent = fmt.Sprintf("could not read: %v", err)
		return
	}
	d.previewContent = string(data)
}

func (d *planDialog) drawSaved(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if w < 40 || h < 8 {
		return
	}
	l, ok := drawSplitPane(scr, area, "plans", fileViewerNavW)
	if !ok {
		return
	}
	d.height = l.Body.Dy()
	left := overlayTopBar("plan", []string{"live", "saved"}, int(d.view), d.savedSummary(), l.Context.Dx())
	drawOverlayContext(scr, l, left, palette.Subtle.On("ctrl+p close "), palette.Border)
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y, l.Nav.Dx(), 1), palette.Muted.On(" saved plans · newest first"))
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+1, l.Nav.Dx(), 1), palette.Border.On(strings.Repeat("─", l.Nav.Dx())))
	navY := l.Nav.Min.Y + 2
	navH := max(0, l.Nav.Dy()-2)
	if len(d.entries) == 0 {
		drawLine(scr, uv.Rect(l.Nav.Min.X, navY, l.Nav.Dx(), 1), palette.Muted.On("  no saved plans"))
	} else {
		start, end := windowAroundCursor(d.cursor, len(d.entries), navH)
		for i := start; i < end; i++ {
			screenY := navY + (i - start)
			label := d.entries[i].name + palette.Subtle.On(" · "+d.entries[i].modTime.Format("2006-01-02 15:04"))
			drawListRow(scr, uv.Rect(l.Nav.Min.X, screenY, l.Nav.Dx(), 1), label, i == d.cursor, true)
		}
	}
	if d.previewContent != "" {
		d.drawPlanContent(scr, l.Detail.Min.X, l.Detail.Min.Y, l.Detail.Dx(), l.Body.Max.Y)
	} else {
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y, l.Detail.Dx(), 1), headerLine("saved plan preview", l.Detail.Dx(), palette.Primary.On))
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+1, l.Detail.Dx(), 1), palette.Muted.On(" status: unavailable"))
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+2, l.Detail.Dx(), 1), palette.Subtle.On(" choose a saved plan to preview its markdown"))
	}
	footer := compactFooterHints(
		keyHint{"↑↓/jk", "navigate"},
		keyHint{"pgup/pgdn", "scroll"},
		keyHint{"tab", "switch view"},
		keyHint{"esc", "close"},
	)
	drawPaneRow(scr, l.Footer, palette.Subtle.On(" "+footer), "")
}

func (d *planDialog) drawPlanContent(scr uv.Screen, x, y, w int, footerY int) {
	cw := w - scrollbarWidth
	bodyW := min(cw-4, maxContentWidth)
	bodyLines := renderMarkdownBlock(bodyW, d.previewContent,
		withCacheKey("plan-preview:"+d.previewName),
	)
	relPath, _ := filepath.Rel(d.workspace, d.previewName)
	relPath = strings.TrimPrefix(relPath, code.PlansDir+"/")
	drawLine(scr, uv.Rect(x, y, cw, 1), headerLine(relPath, cw, palette.Primary.On))
	drawLine(scr, uv.Rect(x, y+1, cw, 1), palette.Subtle.On(" status: loaded · source: saved plan markdown"))
	y += 2
	contentH := footerY - y
	if contentH <= 0 {
		return
	}
	d.scroll = clampScrollOffset(d.scroll, len(bodyLines), contentH)
	for i := d.scroll; i < len(bodyLines) && (i-d.scroll) < contentH; i++ {
		screenY := y + (i - d.scroll)
		drawLine(scr, uv.Rect(x, screenY, cw, 1), "  "+bodyLines[i])
	}
	drawPaneScrollbar(scr, x+w-1, y, contentH, len(bodyLines), d.scroll)
}

// planWrapWidth caps step-text wrapping so a long step doesn't stretch the
// centered box across an ultrawide terminal.
const (
	planWrapWidth    = 64
	planMinBodyWidth = 42
)

func planWrapForArea(area uv.Rectangle) int {
	if area.Dx() <= 0 {
		return planWrapWidth
	}
	return min(planWrapWidth, max(24, area.Dx()-8))
}

// planLines renders the plan as status-coloured step rows (wrapped to width),
// a counts line, and the optional change explanation. Empty plan → a dim hint.
func planLines(p code.Plan, width int) []string {
	if p.IsEmpty() {
		return []string{
			palette.PlanMode.On("no structured plan yet"),
			palette.Subtle.On("switch to PLAN with shift+tab, then ask for a plan"),
			palette.Muted.On("the agent fills this pane via update_plan"),
			"",
			planFooterLine(),
		}
	}
	var out []string
	done, doing, pending := planCounts(p)
	out = append(out,
		palette.PlanMode.On("current plan"),
		planProgressLine(done, len(p.Steps), width),
		palette.Muted.On(fmt.Sprintf(
			"%d steps · %d done · %d in progress · %d pending", len(p.Steps), done, doing, pending)),
		"",
	)

	numW := len(strconv.Itoa(len(p.Steps)))
	for i, s := range p.Steps {
		glyph, style := planStepDecor(s.Status)
		prefix := fmt.Sprintf("%*d. %s ", numW, i+1, glyph)
		out = append(out, renderPlain(width, s.Text,
			withFirstPrefix(prefix, strings.Repeat(" ", ansi.StringWidth(prefix))),
			withStyle(style),
		)...)
	}
	if p.Explanation != "" {
		out = append(out, "")
		out = append(out, renderContentBlock(width, contentBlock{
			kind:  contentPlain,
			text:  "latest update: " + p.Explanation,
			style: palette.Subtle.On,
		})...)
	}
	out = append(out, "", planFooterLine())
	return out
}

func planCounts(p code.Plan) (int, int, int) {
	done, doing, pending := 0, 0, 0
	for _, s := range p.Steps {
		switch s.Status {
		case code.StepStatuses.COMPLETED:
			done++
		case code.StepStatuses.INPROGRESS:
			doing++
		default:
			pending++
		}
	}
	return done, doing, pending
}

func planProgressLine(done, total, width int) string {
	if total <= 0 {
		return palette.Muted.On("progress 0/0")
	}
	barW := min(20, max(8, width-18))
	filled := done * barW / total
	bar := palette.PlanMode.On(strings.Repeat("█", filled)) + palette.Muted.On(strings.Repeat("░", barW-filled))
	return fmt.Sprintf("%s %s", bar, palette.Subtle.On(fmt.Sprintf("%d/%d done", done, total)))
}

func planFooterLine() string {
	return keyLegend(keyHint{"ctrl+p", "close"}, keyHint{"esc", "close"}, keyHint{"q", "close"}, keyHint{"tab", "saved plans"}, keyHint{label: "updates live"})
}

// drawPlanDialogBox renders the live plan as a centered PlanMode-tinted box,
// auto-sized to its content with a minimum body width. It is the shared
// drawDialogBox core with the plan's colours and width floor.
func drawPlanDialogBox(scr uv.Screen, area uv.Rectangle, title string, lines []string) {
	drawDialogBoxColored(scr, area, title, lines, planMinBodyWidth, palette.PlanMode, palette.PlanMode)
}

// planStepDecor maps a step status to its marker glyph + colour styler.
func planStepDecor(st code.StepStatus) (string, func(string) string) {
	switch st {
	case code.StepStatuses.COMPLETED:
		return "✓", palette.Success.On
	case code.StepStatuses.INPROGRESS:
		return "▶", palette.Warning.On
	default:
		return "☐", palette.Subtle.On
	}
}

// planNotice is the one-line transcript marker emitted when the plan changes,
// so the activity log shows progress even with the overlay closed.
func planNotice(p code.Plan) string {
	if p.IsEmpty() {
		return "plan cleared"
	}
	done := 0
	for _, s := range p.Steps {
		if s.Status == code.StepStatuses.COMPLETED {
			done++
		}
	}
	return fmt.Sprintf("plan updated · %d/%d done", done, len(p.Steps))
}
