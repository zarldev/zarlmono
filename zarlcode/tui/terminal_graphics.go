package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// terminalGraphics bridges the event-loop-owned View and Bubble Tea's output
// goroutine. Only immutable encoded frames cross this lock; it starts no worker.
// The program owns its lifetime and the underlying output remains caller-owned.
type terminalGraphics struct {
	mu      sync.Mutex
	frame   string
	text    string
	dirty   bool
	active  bool
	painted string
}

func (g *terminalGraphics) publish(frame, text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.frame != frame || g.text != text {
		// Text-changing frames already cause a renderer write. Only image-only
		// changes need a wake, otherwise Raw would paint before that text diff.
		g.dirty = g.frame != frame && g.text == text
		g.frame, g.text = frame, text
	}
}

func (g *terminalGraphics) refreshCmd() tea.Cmd {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.dirty {
		return nil
	}
	g.dirty = false
	// A changed image can have identical reserved text cells. Force an output
	// write even when Bubble Tea's text diff is empty. This carries no stale
	// image snapshot: the writer always uses the latest published frame.
	return tea.Raw("\x1b7\x1b8")
}

// write appends graphics AFTER terminal text updates, not into View.Content
// (StyledString strips APC), nor before a resize's erase via tea.Raw. Unchanged
// placements survive ordinary text writes; erases and scrolling require a replay.
func (g *terminalGraphics) write(w io.Writer, p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if bytes.Contains(p, []byte("\x1b[?1049l")) {
		if g.painted != "" {
			if _, err := io.WriteString(w, kittyGraphicsClear); err != nil {
				return 0, fmt.Errorf("clear terminal graphics: %w", err)
			}
		}
		g.active, g.painted = false, ""
	} else if bytes.Contains(p, []byte("\x1b[?1049h")) {
		g.active = true
	}
	repaint := g.active && (g.frame != g.painted || (g.frame != "" && terminalGraphicsInvalidated(p)))
	payload := p
	prefix := 0
	if repaint {
		// Extend the renderer's synchronized update through image replacement.
		// Otherwise the terminal can display the gap between delete and transmit,
		// even when both commands arrive in a single write. Image-only wakes need
		// their own synchronized update because they bypass the text renderer.
		text := bytes.TrimSuffix(p, []byte(ansi.ResetModeSynchronizedOutput))
		if !bytes.HasPrefix(text, []byte(ansi.SetModeSynchronizedOutput)) {
			prefix = len(ansi.SetModeSynchronizedOutput)
		}
		var frame bytes.Buffer
		if prefix != 0 {
			frame.WriteString(ansi.SetModeSynchronizedOutput)
		}
		frame.Write(text)
		frame.WriteString(kittyGraphicsClear)
		frame.WriteString(g.frame)
		frame.WriteString(ansi.ResetModeSynchronizedOutput)
		payload = frame.Bytes()
	}
	n, err := w.Write(payload)
	written := min(len(p), max(0, n-prefix))
	if err != nil {
		return written, fmt.Errorf("write terminal frame: %w", err)
	}
	if n != len(payload) {
		return written, io.ErrShortWrite
	}
	if repaint {
		g.painted = g.frame
	}
	return len(p), nil
}

// terminalGraphicsInvalidated recognizes the renderer's screen erases and hard
// scrolling, which can remove or move Kitty placements without a layout change.
// A newline may scroll at the bottom margin, so conservatively replay for it too.
// Decode sequences rather than matching bytes inside OSC titles or other data.
func terminalGraphicsInvalidated(p []byte) bool {
	var state byte
	for len(p) > 0 {
		seq, _, n, next := ansi.DecodeSequence(p, state, nil)
		p, state = p[n:], next
		switch string(seq) {
		case "\n", "\v", "\f", "\x1bD", "\x1bE", "\x1bM", "\x1bc":
			return true
		}
		if bytes.HasPrefix(seq, []byte("\x1b[")) {
			switch seq[len(seq)-1] {
			case 'J', 'L', 'M', 'S', 'T':
				return true
			}
		}
	}
	return false
}

// Embedding the terminal file preserves Fd for Bubble Tea's terminal detection.
// This adapter borrows stdout; Bubble Tea's normal shutdown exits the alternate
// screen, and write clears placements before forwarding that exit sequence.
type terminalGraphicsOutput struct {
	*os.File
	graphics *terminalGraphics
}

func (w terminalGraphicsOutput) Write(p []byte) (int, error) {
	return w.graphics.write(w.File, p)
}

func (w terminalGraphicsOutput) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (m *UI) terminalOutput(file *os.File) io.Writer {
	if !fileViewerTerminalGraphicsEnabled() || !term.IsTerminal(file.Fd()) {
		return file
	}
	m.graphics = &terminalGraphics{}
	return terminalGraphicsOutput{File: file, graphics: m.graphics}
}
