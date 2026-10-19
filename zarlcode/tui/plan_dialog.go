package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

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
//	live  — progress, a selectable step outline, and focused step review
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
	scroll         int       // saved preview scroll offset
	height         int
	liveScroll     int // live-plan body scroll offset
	liveViewport   int
	liveWidth      int // actual step wrapping used by both painting and navigation
	liveCursor     int
	liveSteps      []code.PlanStep // owned text snapshot detects replacement, not status changes
}

func newPlanDialog(plan *code.Plan, workspace string) *planDialog {
	d := &planDialog{plan: plan, workspace: workspace, view: planViewLive, liveWidth: planWrapWidth}
	d.loadEntries()
	d.syncLivePlan()
	return d
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

func (d *planDialog) fullScreen() bool { return true }

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

	if d.view == planViewLive {
		d.handleLiveKey(msg.String())
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
			d.scroll -= max(1, d.height-1)
			if d.scroll < 0 {
				d.scroll = 0
			}
		case "pgdown":
			d.scroll += max(1, d.height-1)
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
	l := drawDetailView(scr, area, "plan", []string{"live", "saved"}, int(d.view))
	if l.Body.Empty() {
		return
	}
	nav, detail := l.Body, l.Body
	if l.Body.Dx() >= 72 {
		navW := min(fileViewerNavW, l.Body.Dx()/3)
		nav.Max.X = nav.Min.X + navW - 1
		detail.Min.X += navW + 3
		for y := l.Body.Min.Y; y < l.Body.Max.Y; y++ {
			drawLine(scr, uv.Rect(l.Body.Min.X+navW, y, 1, 1), palette.Border.On("│"))
		}
	} else {
		// Stack a small, cursor-following list above the preview on narrow screens.
		nav.Max.Y = nav.Min.Y + min(l.Body.Dy(), max(2, min(4, l.Body.Dy()/3)))
		detail.Min.Y = min(l.Body.Max.Y, nav.Max.Y+1)
	}
	drawLine(scr, uv.Rect(nav.Min.X, nav.Min.Y, nav.Dx(), 1), sidebarHeading("saved plans")+palette.Subtle.On(" · newest first"))
	navY, navH := nav.Min.Y+1, max(0, nav.Dy()-1)
	if len(d.entries) == 0 {
		drawLine(scr, uv.Rect(nav.Min.X, navY, nav.Dx(), min(1, navH)), palette.Muted.On("no saved plans"))
	} else {
		start, end := windowAroundCursor(d.cursor, len(d.entries), navH)
		for i := start; i < end; i++ {
			label := d.entries[i].name
			if i == d.cursor {
				label = sidebarHeading(label)
			} else {
				label = palette.Muted.On(label)
			}
			drawListRow(scr, uv.Rect(nav.Min.X, navY+i-start, nav.Dx(), 1), label, i == d.cursor, true)
		}
	}
	d.height = max(0, detail.Dy()-2)
	if !detail.Empty() {
		if d.previewContent != "" {
			d.drawPlanContent(scr, detail.Min.X, detail.Min.Y, detail.Dx(), detail.Max.Y)
		} else {
			drawLine(scr, uv.Rect(detail.Min.X, detail.Min.Y, detail.Dx(), 1), sidebarHeading("saved plan preview"))
			if detail.Dy() > 1 {
				drawLine(scr, uv.Rect(detail.Min.X, detail.Min.Y+1, detail.Dx(), 1), palette.Muted.On("choose a saved plan to preview"))
			}
		}
	}
	hints := "esc close · tab live · ↑↓ plans"
	if l.Footer.Dx() >= 64 {
		hints = "ctrl+p / esc close · tab live · ↑↓ plans · pgup/pgdn scroll"
	}
	drawDetailFooter(scr, l.Footer, hints, d.savedSummary())
}

func (d *planDialog) drawPlanContent(scr uv.Screen, x, y, w int, footerY int) {
	cw := w - scrollbarWidth
	// The standalone saved-plan preview retains its own reading measure.
	const planPreviewWidth = 90
	bodyW := max(1, min(cw, planPreviewWidth))
	bodyLines := renderMarkdownBlock(bodyW, d.previewContent,
		withCacheKey("plan-preview:"+d.previewName),
	)
	relPath, _ := filepath.Rel(d.workspace, d.previewName)
	relPath = strings.TrimPrefix(relPath, code.PlansDir+"/")
	drawLine(scr, uv.Rect(x, y, cw, 1), sidebarHeading(relPath))
	if footerY-y > 1 {
		drawLine(scr, uv.Rect(x, y+1, cw, 1), palette.Subtle.On("source: saved plan markdown"))
	}
	y += 2
	contentH := footerY - y
	if contentH <= 0 {
		return
	}
	d.scroll = clampScrollOffset(d.scroll, len(bodyLines), contentH)
	for i := d.scroll; i < len(bodyLines) && (i-d.scroll) < contentH; i++ {
		screenY := y + (i - d.scroll)
		drawLine(scr, uv.Rect(x, screenY, cw, 1), bodyLines[i])
	}
	drawPaneScrollbar(scr, x+w-1, y, contentH, len(bodyLines), d.scroll)
}

// planWrapWidth retains a readable step measure on wide terminals.
const planWrapWidth = 90

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
	bar := palette.Fg.On(strings.Repeat("━", filled)) + palette.Border.On(strings.Repeat("─", barW-filled))
	return fmt.Sprintf("%s %s", bar, palette.Subtle.On(fmt.Sprintf("%d/%d done", done, total)))
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
