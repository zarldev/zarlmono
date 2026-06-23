package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// steerTray is a full-screen overlay that shows the live injection queue. From it
// the user can edit, delete, or clear queued inputs, and append quick control
// snippets — all while the top-level runner is still active.
type steerTray struct {
	live   *engine.LiveRunner
	cursor int
}

type steerTrayRowKind int

const (
	steerTrayRowMessage steerTrayRowKind = iota
	steerTrayRowClear
	steerTrayRowControl
)

const steerTrayVisibleRows = 12

type steerTrayRow struct {
	kind  steerTrayRowKind
	id    int
	label string
	text  string
}

type steerControlOption struct {
	label string
	text  string
}

var steerControlOptions = []steerControlOption{
	{label: "stop after current tool", text: "stop after current tool"},
	{label: "summarize and stop", text: "summarize and stop"},
	{label: "run or verify tests before finishing", text: "run or verify tests before finishing"},
	{label: "prefer minimal diffs", text: "prefer minimal diffs"},
	{label: "do not touch unrelated files", text: "do not touch unrelated files"},
}

func newSteerTray(live *engine.LiveRunner) *steerTray {
	return &steerTray{live: live}
}

func (t *steerTray) fullScreen() bool { return true }

func (t *steerTray) handleKey(msg tea.KeyPressMsg) action {
	rows := t.rows()
	t.clampCursor(rows)

	switch msg.String() {
	case "esc", "ctrl+y", "q":
		return actionClose{}
	case "up", "k":
		if t.cursor > 0 {
			t.cursor--
		}
	case "down", "j":
		if t.cursor < len(rows)-1 {
			t.cursor++
		}
	case "home", "g":
		t.cursor = 0
	case "end", "G":
		if len(rows) > 0 {
			t.cursor = len(rows) - 1
		}
	case "enter", "space", " ":
		if t == nil || t.live == nil || t.cursor < 0 || t.cursor >= len(rows) {
			return actionNone{}
		}
		row := rows[t.cursor]
		switch row.kind {
		case steerTrayRowMessage:
			return actionPush{d: newQueueEditor(t.live, row.id, row.text)}
		case steerTrayRowClear:
			t.live.QueueClear()
			t.clampCursor(t.rows())
		case steerTrayRowControl:
			t.live.QueueAppendControl(row.text)
			t.clampCursor(t.rows())
		}
	case "e":
		if t != nil && t.live != nil && t.cursor >= 0 && t.cursor < len(rows) {
			row := rows[t.cursor]
			if row.kind == steerTrayRowMessage {
				return actionPush{d: newQueueEditor(t.live, row.id, row.text)}
			}
		}
	case "d":
		if t != nil && t.live != nil && t.cursor >= 0 && t.cursor < len(rows) {
			row := rows[t.cursor]
			if row.kind == steerTrayRowMessage {
				t.live.QueueRemove(row.id)
				t.clampCursor(t.rows())
			}
		}
	case "c":
		if t != nil && t.live != nil {
			t.live.QueueClear()
			t.clampCursor(t.rows())
		}
	}
	return actionNone{}
}

func (t *steerTray) rows() []steerTrayRow {
	var msgs []engine.QueuedMessage
	if t != nil && t.live != nil {
		msgs = t.live.QueueSnapshot()
	}

	rows := make([]steerTrayRow, 0, len(msgs)+len(steerControlOptions)+1)
	for _, msg := range msgs {
		rows = append(rows, steerTrayRow{
			kind:  steerTrayRowMessage,
			id:    msg.ID,
			label: "queued",
			text:  msg.Message.Content,
		})
	}
	if len(msgs) > 0 {
		rows = append(rows, steerTrayRow{kind: steerTrayRowClear, label: "clear all queued messages"})
	}
	for _, opt := range steerControlOptions {
		rows = append(rows, steerTrayRow{
			kind:  steerTrayRowControl,
			label: "queue control: " + opt.label,
			text:  opt.text,
		})
	}
	return rows
}

func (t *steerTray) clampCursor(rows []steerTrayRow) {
	if t == nil {
		return
	}
	if len(rows) == 0 {
		t.cursor = 0
		return
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor >= len(rows) {
		t.cursor = len(rows) - 1
	}
}

func (t *steerTray) queueSummary(rows []steerTrayRow) string {
	queued, controls := 0, 0
	for _, row := range rows {
		switch row.kind {
		case steerTrayRowMessage:
			queued++
		case steerTrayRowControl:
			controls++
		}
	}
	return fmt.Sprintf("%d queued · %d controls", queued, controls)
}

func (t *steerTray) navSummary(rows []steerTrayRow) string {
	if len(rows) == 0 || t.cursor < 0 || t.cursor >= len(rows) {
		return "queue empty"
	}
	row := rows[t.cursor]
	switch row.kind {
	case steerTrayRowMessage:
		return fmt.Sprintf("queued message · id %d", row.id)
	case steerTrayRowClear:
		return "destructive queue action"
	default:
		return "control snippet"
	}
}

func (t *steerTray) detailLines(rows []steerTrayRow, width int) []string {
	out := []string{
		sectionHead("selection", width),
		palette.Muted.On("queue controls are sent as steering messages to the live runner"),
		"",
	}
	if len(rows) == 0 || t.cursor < 0 || t.cursor >= len(rows) {
		return append(out, palette.Muted.On("no queued messages or controls"))
	}
	row := rows[t.cursor]
	kind := "queued message"
	detail := row.text
	actions := "enter/e edit · d delete"
	switch row.kind {
	case steerTrayRowClear:
		kind = "clear queued messages"
		detail = "remove every queued steer message"
		actions = "enter/c clear"
	case steerTrayRowControl:
		kind = "control snippet"
		detail = row.text
		actions = "enter send control"
	}
	out = append(out,
		palette.Subtle.On("kind ")+palette.Muted.On(kind),
		palette.Subtle.On("effect ")+palette.Muted.On(detail),
		palette.Subtle.On("actions ")+palette.Muted.On(actions),
	)
	if row.kind == steerTrayRowMessage {
		out = append(out, "", sectionHead("message", width))
		out = append(out, renderPlain(width, row.text, withFirstPrefix("  ", "  "), withStyle(palette.Muted.On))...)
	}
	return out
}

func (t *steerTray) draw(scr uv.Screen, area uv.Rectangle) {
	rows := t.rows()
	t.clampCursor(rows)
	w, h := area.Dx(), area.Dy()
	if w < 60 || h < 12 {
		return
	}
	l, ok := drawSplitPane(scr, area, "execution tray", 42)
	if !ok {
		return
	}
	left := overlayTopBar("live controls", []string{"queue", "controls"}, 0, t.queueSummary(rows), l.Context.Dx())
	drawOverlayContext(scr, l, left, palette.Subtle.On("ctrl+y close "), palette.Border)
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y, l.Nav.Dx(), 1), ansi.Truncate(palette.Muted.On(" "+t.navSummary(rows)), l.Nav.Dx(), ""))
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+1, l.Nav.Dx(), 1), palette.Border.On(strings.Repeat("─", l.Nav.Dx())))
	navY := l.Nav.Min.Y + 2
	navH := max(0, l.Nav.Dy()-2)
	start, end := windowAroundCursor(t.cursor, len(rows), navH)
	if len(rows) == 0 {
		drawLine(scr, uv.Rect(l.Nav.Min.X, navY, l.Nav.Dx(), 1), palette.Muted.On("  no queued messages or controls"))
	} else {
		for i := start; i < end; i++ {
			row := rows[i]
			label := row.label
			switch row.kind {
			case steerTrayRowMessage:
				label = palette.Assistant.On("msg") + " " + row.text
			case steerTrayRowClear:
				label = palette.Warning.On("clear") + " queued messages"
			case steerTrayRowControl:
				label = palette.Info.On("ctl") + " " + row.label
			}
			drawListRow(scr, uv.Rect(l.Nav.Min.X, navY+(i-start), l.Nav.Dx(), 1), label, i == t.cursor, true)
		}
	}
	lines := t.detailLines(rows, l.Detail.Dx())
	for i, ln := range lines {
		if i >= l.Detail.Dy() {
			break
		}
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+i, l.Detail.Dx(), 1), ansi.Truncate(ln, l.Detail.Dx(), ""))
	}
	footer := compactFooterHints(
		keyHint{"↑↓/jk", "navigate"},
		keyHint{"enter", "edit/send"},
		keyHint{"e", "edit msg"},
		keyHint{"d", "delete msg"},
		keyHint{"c", "clear queue"},
		keyHint{"esc", "close"},
	)
	drawPaneRow(scr, l.Footer, palette.Subtle.On(" "+footer), "")
}

type queueEditor struct {
	live  *engine.LiveRunner
	text  string
	msgID int
}

func newQueueEditor(live *engine.LiveRunner, msgID int, text string) *queueEditor {
	return &queueEditor{live: live, msgID: msgID, text: text}
}

func (e *queueEditor) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc":
		return actionClose{}
	case "enter":
		if e.msgID != 0 && e.text != "" {
			e.live.QueueUpdate(e.msgID, e.text)
		}
		return actionClose{}
	case "backspace":
		if len(e.text) > 0 {
			e.text = e.text[:len(e.text)-1]
		}
	default:
		if msg.Text != "" {
			e.text += msg.Text
		}
	}
	return actionNone{}
}

func (e *queueEditor) draw(scr uv.Screen, area uv.Rectangle) {
	lines := []string{
		palette.Primary.On("edit queued message"),
		fmt.Sprintf(" %s", ansi.Truncate(e.text, 60, "...")),
		"",
		palette.Subtle.On("enter") + palette.Muted.On(" save"),
		palette.Subtle.On("esc") + palette.Muted.On(" cancel"),
	}
	drawDialogBox(scr, area, "queue editor", lines)
}
