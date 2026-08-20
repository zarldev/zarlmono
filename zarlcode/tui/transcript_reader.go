package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// transcriptReader is a full-screen, stable-width view over the current
// transcript. It owns independent navigation state while sharing immutable
// item objects with the live timeline.
type transcriptReader struct {
	view      timeline
	query     string
	searching bool
}

func newTranscriptReader(source *timeline) *transcriptReader {
	r := &transcriptReader{}
	if source != nil {
		r.view = *source
		r.view.items = append([]item(nil), source.items...)
	}
	r.view.cache = make(map[item]cacheEntry)
	r.view.geometry = timelineGeometry{dirty: 0}
	r.view.browsing = true
	r.view.sel = max(0, len(r.view.items)-1)
	r.view.selLocal = 0
	r.view.selection = transcriptSelection{}
	return r
}

func (*transcriptReader) fullScreen() bool { return true }

func (r *transcriptReader) handlePaste(text string) {
	if r.searching {
		r.query += strings.ReplaceAll(text, "\n", " ")
	}
}

func (r *transcriptReader) handleKey(msg tea.KeyPressMsg) action {
	if r.searching {
		switch msg.String() {
		case "esc":
			r.searching = false
		case "enter":
			r.searching = false
			r.findNext(1)
		case "backspace":
			if len(r.query) > 0 {
				runes := []rune(r.query)
				r.query = string(runes[:len(runes)-1])
			}
		default:
			if msg.Text != "" {
				r.query += msg.Text
			}
		}
		return actionNone{}
	}
	if r.view.selectionActive() {
		switch msg.String() {
		case "esc":
			r.view.cancelSelection()
		case "up", "k":
			r.view.moveSelection(-1)
		case "down", "j":
			r.view.moveSelection(1)
		case "pgup":
			r.view.moveSelection(-r.view.pageStep())
		case "pgdown":
			r.view.moveSelection(r.view.pageStep())
		case "y":
			return actionCopyText{text: r.view.finishSelection()}
		}
		return actionNone{}
	}
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "up", "k":
		r.view.cursorUp()
	case "down", "j":
		r.view.cursorDown()
		r.view.browsing = true
	case "pgup":
		r.view.pageUp()
	case "pgdown":
		r.view.scrollLines(r.view.pageStep())
	case "home", "g":
		r.view.cursorTop()
	case "end", "G":
		r.view.sel = max(0, len(r.view.items)-1)
		r.view.selLocal = 0
		r.view.scrollToSel()
	case "[":
		r.jumpUser(-1)
	case "]":
		r.jumpUser(1)
	case "ctrl+f":
		r.searching = true
		r.query = ""
	case "n":
		r.findNext(1)
	case "N":
		r.findNext(-1)
	case "v":
		r.view.startSelection()
	case "Y":
		return actionCopyText{text: r.currentMessageText()}
	case "enter", "space", " ":
		r.view.toggleSelected()
	}
	return actionNone{}
}

func (r *transcriptReader) jumpUser(direction int) {
	for i := r.view.sel + direction; i >= 0 && i < len(r.view.items); i += direction {
		switch r.view.items[i].(type) {
		case *userItem, *queuedUserItem:
			r.view.sel, r.view.selLocal = i, 0
			r.view.scrollToSel()
			return
		}
	}
}

func (r *transcriptReader) findNext(direction int) {
	needle := strings.ToLower(strings.TrimSpace(r.query))
	if needle == "" || len(r.view.items) == 0 {
		return
	}
	for step := 1; step <= len(r.view.items); step++ {
		i := (r.view.sel + direction*step) % len(r.view.items)
		if i < 0 {
			i += len(r.view.items)
		}
		if strings.Contains(strings.ToLower(r.itemText(i)), needle) {
			r.view.sel, r.view.selLocal = i, 0
			r.view.scrollToSel()
			return
		}
	}
}

func (r *transcriptReader) itemText(i int) string {
	if i < 0 || i >= len(r.view.items) {
		return ""
	}
	lines := r.view.renderItem(r.view.items[i], r.view.lwidth())
	for i := range lines {
		lines[i] = cleanTranscriptCopyLine(r.view.items[i], lines[i])
	}
	return strings.Join(lines, "\n")
}

func (r *transcriptReader) currentMessageText() string { return r.itemText(r.view.sel) }

func (r *transcriptReader) draw(scr uv.Screen, area uv.Rectangle) {
	inner := drawPaneFrameColored(scr, area, "TRANSCRIPT READER", palette.Border, palette.Primary)
	if inner.Empty() {
		return
	}
	body := uv.Rect(inner.Min.X, inner.Min.Y, inner.Dx(), max(0, inner.Dy()-2))
	r.view.viewWidth, r.view.viewHeight = body.Dx(), body.Dy()
	lines := r.view.renderBrowse(body.Dx(), body.Dy())
	for i, line := range lines {
		drawLine(scr, uv.Rect(body.Min.X, body.Min.Y+i, body.Dx(), 1), line)
	}
	footer := "↑↓ navigate  [ ] user turns  ctrl+f search  v select  y copy range  Y copy message  esc close"
	if r.searching {
		footer = "search: " + r.query + "█"
	} else if r.query != "" {
		footer = "search: " + r.query + "  ·  n/N next/previous  ·  " + footer
	}
	drawLine(scr, uv.Rect(inner.Min.X, inner.Max.Y-1, inner.Dx(), 1), palette.Muted.On(ansi.Truncate(footer, inner.Dx(), "…")))
}
