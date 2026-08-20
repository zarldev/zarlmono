package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/db"
)

const toolHistoryNavW = 34

// toolHistory is the read-only overlay (ctrl+h) listing a session's captured
// tool results and rendering each one's full, untruncated output. Newest calls
// are listed first; the cursor starts on the most recent. Output bodies are
// loaded lazily on selection rather than eagerly at open.
type toolHistory struct {
	store     *db.Store
	sessionID string
	summaries []db.ToolOutputSummary
	full      map[string]db.ToolOutputRecord
	cursor    int
	scroll    int
	status    string
}

// newToolHistory loads the session's tool-call metadata, newest first, plus the
// full body of the most recent call. A nil store or empty session identity
// yields an empty viewer with an orientation status rather than an error.
func newToolHistory(store *db.Store, sessionID string) *toolHistory {
	h := &toolHistory{store: store, sessionID: sessionID, full: map[string]db.ToolOutputRecord{}}
	if store == nil || sessionID == "" {
		h.status = "no session yet — run a task first"
		return h
	}
	summaries, err := store.ListToolOutputSummariesBySession(context.Background(), sessionID)
	if err != nil {
		h.status = "tool history: " + err.Error()
		return h
	}
	for i, j := 0, len(summaries)-1; i < j; i, j = i+1, j-1 {
		summaries[i], summaries[j] = summaries[j], summaries[i]
	}
	h.summaries = summaries
	h.loadAt(h.cursor)
	return h
}

// loadAt fetches and caches the full output body for the summary at index i.
// Best-effort: a fetch failure leaves the entry absent and the detail pane
// reports it.
func (h *toolHistory) loadAt(i int) {
	if h.store == nil || h.sessionID == "" || i < 0 || i >= len(h.summaries) {
		return
	}
	id := h.summaries[i].ToolCallID
	if _, ok := h.full[id]; ok {
		return
	}
	if rec, err := h.store.GetToolOutput(context.Background(), h.sessionID, id); err == nil {
		h.full[id] = rec
	}
}

func (h *toolHistory) selected() (db.ToolOutputRecord, bool) {
	if h.cursor < 0 || h.cursor >= len(h.summaries) {
		return db.ToolOutputRecord{}, false
	}
	rec, ok := h.full[h.summaries[h.cursor].ToolCallID]
	return rec, ok
}

func (h *toolHistory) fullScreen() bool { return true }

func (h *toolHistory) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "q", "ctrl+h":
		return actionClose{}
	case "up", "k":
		if h.cursor > 0 {
			h.cursor--
			h.scroll = 0
			h.loadAt(h.cursor)
		}
		return actionNone{}
	case "down", "j":
		if h.cursor < len(h.summaries)-1 {
			h.cursor++
			h.scroll = 0
			h.loadAt(h.cursor)
		}
		return actionNone{}
	case "home", "g":
		h.scroll = 0
	case "pgup":
		h.scroll -= 10
		if h.scroll < 0 {
			h.scroll = 0
		}
	case "pgdown":
		h.scroll += 10
	}
	return actionNone{}
}

func (h *toolHistory) draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() < 50 || area.Dy() < 8 {
		return
	}
	l, ok := drawSplitPane(scr, area, "tool history", toolHistoryNavW)
	if !ok {
		return
	}
	left := overlayTopBar("tool history", nil, 0, fmt.Sprintf("%d calls", len(h.summaries)), l.Context.Dx())
	drawOverlayContext(scr, l, left, palette.Subtle.On("ctrl+h close "), palette.Border)
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y, l.Nav.Dx(), 1), palette.Muted.On(" calls · newest first"))
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+1, l.Nav.Dx(), 1), palette.Border.On(strings.Repeat("─", l.Nav.Dx())))

	for i, r := range h.summaries {
		screenY := l.Nav.Min.Y + 2 + i
		if screenY >= l.Nav.Max.Y {
			break
		}
		label := fmt.Sprintf("%-14s %s", r.ToolName, r.CreatedAt.Format("15:04:05"))
		drawListRow(scr, uv.Rect(l.Nav.Min.X, screenY, l.Nav.Dx(), 1), label, i == h.cursor, true)
	}

	detailY := l.Detail.Min.Y
	detailH := l.Detail.Dy()
	cw := l.Detail.Dx() - scrollbarWidth
	lines := h.detailLines(cw)
	h.scroll = clampScrollOffset(h.scroll, len(lines), detailH)
	for i := h.scroll; i < len(lines) && i-h.scroll < detailH; i++ {
		if strings.HasPrefix(ansi.Strip(lines[i]), "├") {
			drawSectionRule(scr, l.Detail, detailY+i-h.scroll, lines[i])
			continue
		}
		drawLine(scr, uv.Rect(l.Detail.Min.X, detailY+i-h.scroll, cw, 1), ansi.Truncate(lines[i], cw, ""))
	}
	drawPaneScrollbar(scr, l.Detail.Max.X-1, detailY, detailH, len(lines), h.scroll)
	h.drawFooter(scr, l.Footer)
}

func (h *toolHistory) detailLines(width int) []string {
	if len(h.summaries) == 0 {
		if h.status != "" {
			return []string{palette.Muted.On(" " + h.status)}
		}
		return []string{palette.Muted.On(" no tool calls recorded yet — run a task first")}
	}
	r, ok := h.selected()
	if !ok {
		return []string{palette.Muted.On(" failed to load output for " + h.summaries[h.cursor].ToolCallID)}
	}
	out := []string{sectionHead(r.ToolName, width)}
	if r.ArgsJSON != "" && r.ArgsJSON != "null" {
		out = append(out, palette.Subtle.On("args ")+palette.Muted.On(r.ArgsJSON))
	}
	out = append(out, palette.Subtle.On("call "+r.ToolCallID), "")
	out = append(out, renderPlain(width, r.Output, withStyle(palette.Muted.On))...)
	return out
}

func (h *toolHistory) drawFooter(scr uv.Screen, r uv.Rectangle) {
	hints := []keyHint{{"↑↓/jk", "select call"}, {"pgup/pgdn", "page"}, {"esc", "close"}}
	drawPaneRow(scr, r, palette.Subtle.On(" "+keyLegend(hints...)), "")
}
