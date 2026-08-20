package tui

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const fileMentionLimit = 20000

type fileMentionPicker struct {
	root   string
	files  []string
	query  string
	cursor int
}

func newFileMentionPicker(root string) *fileMentionPicker {
	p := &fileMentionPicker{root: root}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".zarlcode", ".zarlcodeold", "node_modules", "dist":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if len(p.files) >= fileMentionLimit {
			return fs.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			p.files = append(p.files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(p.files)
	return p
}

func (p *fileMentionPicker) filtered() []string {
	query := strings.ToLower(strings.TrimSpace(p.query))
	if query == "" {
		return p.files
	}
	parts := strings.Fields(query)
	out := make([]string, 0, min(len(p.files), 100))
	for _, path := range p.files {
		candidate := strings.ToLower(path)
		match := true
		for _, part := range parts {
			if !strings.Contains(candidate, part) {
				match = false
				break
			}
		}
		if match {
			out = append(out, path)
		}
	}
	return out
}

func (p *fileMentionPicker) handlePaste(text string) { p.query += strings.ReplaceAll(text, "\n", " ") }

func (p *fileMentionPicker) handleKey(msg tea.KeyPressMsg) action {
	files := p.filtered()
	switch msg.String() {
	case "esc":
		return actionClose{}
	case "enter":
		if len(files) > 0 {
			p.cursor = min(p.cursor, len(files)-1)
			return actionAttachFile{path: filepath.Join(p.root, filepath.FromSlash(files[p.cursor]))}
		}
	case "up", "ctrl+p":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "ctrl+n":
		if p.cursor < len(files)-1 {
			p.cursor++
		}
	case "backspace":
		if len(p.query) > 0 {
			runes := []rune(p.query)
			p.query = string(runes[:len(runes)-1])
			p.cursor = 0
		}
	default:
		if msg.Text != "" {
			p.query += msg.Text
			p.cursor = 0
		}
	}
	return actionNone{}
}

func (p *fileMentionPicker) draw(scr uv.Screen, area uv.Rectangle) {
	w := min(84, max(30, area.Dx()-8))
	h := min(24, max(8, area.Dy()-6))
	r := uv.Rect(area.Min.X+(area.Dx()-w)/2, area.Min.Y+(area.Dy()-h)/2, w, h)
	inner := drawPaneFrameColored(scr, r, "ATTACH @FILE", palette.Border, palette.Primary)
	drawLine(scr, uv.Rect(inner.Min.X, inner.Min.Y, inner.Dx(), 1), palette.Fg.On("@"+p.query+"█"))
	files := p.filtered()
	p.cursor = min(p.cursor, max(0, len(files)-1))
	start := max(0, p.cursor-(inner.Dy()-3)+1)
	for row, path := range files[start:min(len(files), start+inner.Dy()-3)] {
		prefix := "  "
		if start+row == p.cursor {
			prefix = "› "
		}
		drawLine(scr, uv.Rect(inner.Min.X, inner.Min.Y+1+row, inner.Dx(), 1), ansi.Truncate(prefix+path, inner.Dx(), "…"))
	}
	drawLine(scr, uv.Rect(inner.Min.X, inner.Max.Y-1, inner.Dx(), 1), palette.Muted.On("type to filter · ↑↓ select · enter attach · esc cancel"))
}
