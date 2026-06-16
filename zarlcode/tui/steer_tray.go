package tui

import (
	"fmt"

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

func (t *steerTray) draw(scr uv.Screen, area uv.Rectangle) {
	var msgs []engine.QueuedMessage
	if t != nil && t.live != nil {
		msgs = t.live.QueueSnapshot()
	}
	rows := t.rows()
	t.clampCursor(rows)

	start := 0
	if t.cursor >= steerTrayVisibleRows {
		start = t.cursor - steerTrayVisibleRows + 1
	}
	end := start + steerTrayVisibleRows
	if end > len(rows) {
		end = len(rows)
	}

	lines := make([]string, 0, steerTrayVisibleRows+7)
	lines = append(lines,
		palette.Primary.On("execution tray (ctrl+y close)"),
		fmt.Sprintf(" %d queued message(s) · %d control option(s)", len(msgs), len(steerControlOptions)),
		"",
	)
	for i := start; i < end; i++ {
		row := rows[i]
		prefix := "  "
		style := palette.Subtle
		if i == t.cursor {
			prefix = "▸ "
			style = palette.Primary
		}

		label := row.label
		switch row.kind {
		case steerTrayRowMessage:
			label = fmt.Sprintf("%d. %s", i+1, ansi.Truncate(row.text, 60, "..."))
		case steerTrayRowClear:
			label = "clear all queued messages"
		case steerTrayRowControl:
			label = row.label
		}
		lines = append(lines, style.On(prefix+label))
	}
	if start > 0 || end < len(rows) {
		lines = append(lines, palette.Muted.On(fmt.Sprintf(" showing %d-%d of %d", start+1, end, len(rows))))
	}
	lines = append(lines, "",
		palette.Subtle.On("↑↓ / j k")+palette.Muted.On(" select"),
		palette.Subtle.On("enter")+palette.Muted.On(" edit selected message / append selected control"),
		palette.Subtle.On("e")+palette.Muted.On(" edit message")+palette.Muted.On("  ")+palette.Subtle.On("d")+palette.Muted.On(" delete message")+palette.Muted.On("  ")+palette.Subtle.On("c")+palette.Muted.On(" clear queue"),
		"",
		palette.Muted.On("Control options are queued as mid-run steering messages."),
	)
	drawDialogBox(scr, area, "execution tray", lines)
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
