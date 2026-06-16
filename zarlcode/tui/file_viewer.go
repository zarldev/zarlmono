package tui

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
)

// fileViewer is a full-screen read-only browser with four tabs: Files,
// Skills, Agents, and Hooks. A directory listing or name list on the left, a
// content preview on the right. Pushed over the main cockpit; Esc/ctrl+f/q
// pops back.
type fileViewer struct {
	workspaceDir string // root for relative-path display
	currentDir   string // absolute path of the displayed directory
	entries      []os.DirEntry
	cursor       int // selected entry index
	scroll       int // content scroll offset
	viewingFile  string
	fileContent  string // cached content of the previewed file
	dirPreview   fileViewerDirPreview
	height       int
	width        int

	mode   fileViewerMode
	skills []catalog.Skill
	agents []catalog.Agent
	hooks  []catalog.Hook
}

type fileViewerMode int

const (
	fileViewerFiles fileViewerMode = iota
	fileViewerSkills
	fileViewerAgents
	fileViewerHooks
	fileViewerModeCount
)

var fileViewerModeNames = []string{"files", "skills", "agents", "hooks"}

const fileViewerNavW = 34 // width of the left directory panel

const (
	fileViewerMaxPreviewBytes = 128 * 1024
	fileViewerMaxPreviewLines = 2000
	fileViewerMaxLineRunes    = 4096
	fileViewerDirPreviewLimit = 20
)

type fileViewerDirPreview struct {
	path      string
	entries   []fileViewerPreviewEntry
	truncated bool
	err       string
}

type fileViewerPreviewEntry struct {
	name  string
	isDir bool
}

func newFileViewer(workspaceDir string) *fileViewer {
	skills, _ := catalog.LoadSkills(workspaceDir)
	agents, _ := catalog.LoadAgents(workspaceDir)
	hooks, _ := catalog.LoadHooks(workspaceDir)
	v := &fileViewer{
		workspaceDir: workspaceDir,
		currentDir:   workspaceDir,
		skills:       skills,
		agents:       agents,
		hooks:        hooks,
	}
	v.loadEntries()
	v.tryPreview()
	return v
}

func newFileViewerAt(workspaceDir, path string) *fileViewer {
	v := newFileViewer(workspaceDir)
	v.openPath(path)
	return v
}

func (fileViewer) fullScreen() bool { return true }

// ─── key handling ──────────────────────────────────────────────────────────

func (v *fileViewer) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "ctrl+f", "q":
		return actionClose{}

	case "tab":
		v.mode = (v.mode + 1) % fileViewerModeCount
		v.resetForMode()
	case "shift+tab":
		v.mode = (v.mode + fileViewerModeCount - 1) % fileViewerModeCount
		v.resetForMode()

	case "up", "k":
		if v.cursor > 0 {
			v.cursor--
			v.tryPreview()
		}

	case "down", "j":
		if v.cursor < v.itemCount()-1 {
			v.cursor++
			v.tryPreview()
		}

	case "enter":
		switch v.mode {
		case fileViewerFiles:
			if v.cursor >= 0 && v.cursor < len(v.entries) {
				e := v.entries[v.cursor]
				if e.IsDir() {
					v.navigateInto(e.Name())
				}
			}
		case fileViewerSkills:
			if s, ok := v.selectedSkill(); ok {
				return actionEditFile{path: s.Source}
			}
		case fileViewerAgents:
			if a, ok := v.selectedAgent(); ok {
				return actionEditFile{path: a.Source}
			}
		case fileViewerHooks:
			if h, ok := v.selectedHook(); ok {
				return actionEditFile{path: h.Source}
			}
		}

	case "e":
		switch v.mode {
		case fileViewerFiles:
			if path, ok := v.selectedFilePath(); ok {
				return actionEditFile{path: path}
			}
		case fileViewerSkills:
			if s, ok := v.selectedSkill(); ok {
				return actionEditFile{path: s.Source}
			}
		case fileViewerAgents:
			if a, ok := v.selectedAgent(); ok {
				return actionEditFile{path: a.Source}
			}
		case fileViewerHooks:
			if h, ok := v.selectedHook(); ok {
				return actionEditFile{path: h.Source}
			}
		}

	case "backspace", "left":
		if v.mode == fileViewerFiles {
			v.navigateUp()
		}

	case "pgup":
		v.scroll -= max(1, v.height-4)
		if v.scroll < 0 {
			v.scroll = 0
		}

	case "pgdown":
		v.scroll += max(1, v.height-4)

	case "home", "g":
		if v.cursor > 0 {
			v.cursor = 0
			v.tryPreview()
		} else {
			v.scroll = 0
		}

	case "end":
		if n := v.itemCount(); n > 0 {
			v.cursor = n - 1
			v.tryPreview()
		}
	}
	return actionNone{}
}

func (v *fileViewer) selectedFilePath() (string, bool) {
	if v.cursor < 0 || v.cursor >= len(v.entries) {
		return "", false
	}
	e := v.entries[v.cursor]
	if e.IsDir() {
		return "", false
	}
	return filepath.Join(v.currentDir, e.Name()), true
}

func (v *fileViewer) resetForMode() {
	v.cursor = 0
	v.scroll = 0
	v.viewingFile = ""
	v.fileContent = ""
	v.dirPreview = fileViewerDirPreview{}
	if v.mode == fileViewerFiles {
		v.loadEntries()
	}
	v.tryPreview()
}

func (v *fileViewer) itemCount() int {
	switch v.mode {
	case fileViewerFiles:
		return len(v.entries)
	case fileViewerSkills:
		return len(v.skills)
	case fileViewerAgents:
		return len(v.agents)
	case fileViewerHooks:
		return len(v.hooks)
	}
	return 0
}

func (v *fileViewer) selectedSkill() (catalog.Skill, bool) {
	if v.mode != fileViewerSkills || v.cursor < 0 || v.cursor >= len(v.skills) {
		return catalog.Skill{}, false
	}
	return v.skills[v.cursor], true
}

func (v *fileViewer) selectedAgent() (catalog.Agent, bool) {
	if v.mode != fileViewerAgents || v.cursor < 0 || v.cursor >= len(v.agents) {
		return catalog.Agent{}, false
	}
	return v.agents[v.cursor], true
}

func (v *fileViewer) selectedHook() (catalog.Hook, bool) {
	if v.mode != fileViewerHooks || v.cursor < 0 || v.cursor >= len(v.hooks) {
		return catalog.Hook{}, false
	}
	return v.hooks[v.cursor], true
}

func (v *fileViewer) refreshEditedPath(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if filepath.Clean(path) == filepath.Clean(v.viewingFile) {
		v.viewingFile = ""
		v.fileContent = ""
	}
	v.openPath(path)
}

// ─── navigation ────────────────────────────────────────────────────────────

func (v *fileViewer) navigateInto(name string) {
	next := filepath.Join(v.currentDir, name)
	info, err := os.Stat(next)
	if err != nil || !info.IsDir() {
		return
	}
	v.currentDir = next
	v.cursor = 0
	v.scroll = 0
	v.viewingFile = ""
	v.fileContent = ""
	v.loadEntries()
	v.tryPreview()
}

func (v *fileViewer) navigateUp() {
	parent := filepath.Dir(v.currentDir)
	// Don't escape the workspace root.
	rel, err := filepath.Rel(v.workspaceDir, parent)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	v.currentDir = parent
	v.cursor = 0
	v.scroll = 0
	v.viewingFile = ""
	v.fileContent = ""
	v.loadEntries()
	v.tryPreview()
}

func (v *fileViewer) loadEntries() {
	entries, err := os.ReadDir(v.currentDir)
	if err != nil {
		v.entries = nil
		return
	}
	// Sort: directories first, then files, each alphabetically.
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
	})
	v.entries = entries
	if v.cursor >= len(v.entries) {
		v.cursor = max(0, len(v.entries)-1)
	}
}

func (v *fileViewer) openPath(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(v.workspaceDir, path)
	}
	fullPath = filepath.Clean(fullPath)
	rel, err := filepath.Rel(v.workspaceDir, fullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return
	}
	if info.IsDir() {
		v.currentDir = fullPath
		v.cursor = 0
		v.scroll = 0
		v.viewingFile = ""
		v.fileContent = ""
		v.loadEntries()
		v.tryPreview()
		return
	}
	v.currentDir = filepath.Dir(fullPath)
	v.cursor = 0
	v.scroll = 0
	v.viewingFile = ""
	v.fileContent = ""
	v.loadEntries()
	base := filepath.Base(fullPath)
	for i, e := range v.entries {
		if !e.IsDir() && e.Name() == base {
			v.cursor = i
			break
		}
	}
	v.tryPreview()
}

// ─── preview ───────────────────────────────────────────────────────────────

func (v *fileViewer) tryPreview() {
	switch v.mode {
	case fileViewerFiles:
		v.tryPreviewFile()
	case fileViewerSkills:
		v.tryPreviewSkill()
	case fileViewerAgents:
		v.tryPreviewAgent()
	case fileViewerHooks:
		v.tryPreviewHook()
	}
}

func (v *fileViewer) tryPreviewFile() {
	if v.cursor < 0 || v.cursor >= len(v.entries) {
		return
	}
	e := v.entries[v.cursor]
	if e.IsDir() {
		v.viewingFile = ""
		v.fileContent = ""
		v.scroll = 0
		v.loadDirPreview(filepath.Join(v.currentDir, e.Name()))
		return
	}
	fullPath := filepath.Join(v.currentDir, e.Name())
	if fullPath == v.viewingFile {
		return // already loaded
	}
	v.viewingFile = fullPath
	v.scroll = 0
	data, truncated, size, err := readFileViewerPreview(fullPath)
	if err != nil {
		v.fileContent = fmt.Sprintf("could not read: %v", err)
		return
	}
	if fileViewerLooksBinary(data) {
		v.fileContent = fmt.Sprintf("binary file preview skipped (%s)", humanBytes(size))
		return
	}
	content, longLineTruncated := truncateFileViewerLongLines(string(data))
	content, lineTruncated := truncateFileViewerLines(content)
	if truncated {
		content += fmt.Sprintf("\n\n… preview truncated after %s (file is %s)", humanBytes(fileViewerMaxPreviewBytes), humanBytes(size))
	}
	if longLineTruncated {
		content += fmt.Sprintf("\n\n… long lines truncated after %d characters", fileViewerMaxLineRunes)
	}
	if lineTruncated {
		content += fmt.Sprintf("\n\n… preview truncated after %d lines", fileViewerMaxPreviewLines)
	}
	v.fileContent = content
}

func (v *fileViewer) tryPreviewSkill() {
	if v.cursor < 0 || v.cursor >= len(v.skills) {
		v.fileContent = ""
		return
	}
	s := v.skills[v.cursor]
	v.viewingFile = s.Source
	v.scroll = 0
	c := s.Body
	if c == "" {
		c = "(empty)"
	}
	content, longLineTruncated := truncateFileViewerLongLines(c)
	content, lineTruncated := truncateFileViewerLines(content)
	if longLineTruncated {
		content += fmt.Sprintf("\n\n… long lines truncated after %d characters", fileViewerMaxLineRunes)
	}
	if lineTruncated {
		content += fmt.Sprintf("\n\n… preview truncated after %d lines", fileViewerMaxPreviewLines)
	}
	v.fileContent = content
}

func (v *fileViewer) tryPreviewAgent() {
	if v.cursor < 0 || v.cursor >= len(v.agents) {
		v.fileContent = ""
		return
	}
	a := v.agents[v.cursor]
	v.viewingFile = a.Source
	v.scroll = 0
	c := a.Body
	if c == "" {
		c = "(empty)"
	}
	content, longLineTruncated := truncateFileViewerLongLines(c)
	content, lineTruncated := truncateFileViewerLines(content)
	if longLineTruncated {
		content += fmt.Sprintf("\n\n… long lines truncated after %d characters", fileViewerMaxLineRunes)
	}
	if lineTruncated {
		content += fmt.Sprintf("\n\n… preview truncated after %d lines", fileViewerMaxPreviewLines)
	}
	v.fileContent = content
}

func (v *fileViewer) tryPreviewHook() {
	if v.cursor < 0 || v.cursor >= len(v.hooks) {
		v.fileContent = ""
		return
	}
	h := v.hooks[v.cursor]
	v.viewingFile = h.Source
	v.scroll = 0
	// Lead with the trigger config so the script below reads in context.
	meta := []string{"event: " + string(h.Event)}
	if h.Matcher != "" {
		meta = append(meta, "matcher: "+h.Matcher)
	}
	meta = append(meta, fmt.Sprintf("blocking: %t", h.Blocking), "timeout: "+h.Timeout.String())
	c := strings.Join(meta, "\n") + "\n\n" + h.Command
	content, longLineTruncated := truncateFileViewerLongLines(c)
	content, lineTruncated := truncateFileViewerLines(content)
	if longLineTruncated {
		content += fmt.Sprintf("\n\n… long lines truncated after %d characters", fileViewerMaxLineRunes)
	}
	if lineTruncated {
		content += fmt.Sprintf("\n\n… preview truncated after %d lines", fileViewerMaxPreviewLines)
	}
	v.fileContent = content
}

func (v *fileViewer) loadDirPreview(dirPath string) {
	if dirPath == v.dirPreview.path {
		return
	}
	v.dirPreview = fileViewerDirPreview{path: dirPath}
	f, err := os.Open(dirPath)
	if err != nil {
		v.dirPreview.err = err.Error()
		return
	}
	defer f.Close()
	entries, err := f.ReadDir(fileViewerDirPreviewLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		v.dirPreview.err = err.Error()
		return
	}
	if len(entries) > fileViewerDirPreviewLimit {
		v.dirPreview.truncated = true
		entries = entries[:fileViewerDirPreviewLimit]
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name()))
	})
	v.dirPreview.entries = make([]fileViewerPreviewEntry, 0, len(entries))
	for _, e := range entries {
		v.dirPreview.entries = append(v.dirPreview.entries, fileViewerPreviewEntry{name: e.Name(), isDir: e.IsDir()})
	}
}

func readFileViewerPreview(path string) ([]byte, bool, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, false, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(path)
		if err != nil {
			return nil, false, 0, err
		}
	}
	if !info.Mode().IsRegular() {
		return nil, false, info.Size(), errors.New("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, info.Size(), err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, fileViewerMaxPreviewBytes+1))
	if err != nil {
		return nil, false, info.Size(), err
	}
	truncated := len(data) > fileViewerMaxPreviewBytes
	if truncated {
		data = data[:fileViewerMaxPreviewBytes]
	}
	return data, truncated || info.Size() > int64(fileViewerMaxPreviewBytes), info.Size(), nil
}

func fileViewerLooksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if !utf8.Valid(sample) {
		return true
	}
	control := 0
	for _, b := range sample {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			control++
		}
	}
	return control*100/len(sample) > 30
}

func truncateFileViewerLines(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) <= fileViewerMaxPreviewLines {
		return content, false
	}
	return strings.Join(lines[:fileViewerMaxPreviewLines], "\n"), true
}

func truncateFileViewerLongLines(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	truncated := false
	for i, line := range lines {
		if utf8.RuneCountInString(line) <= fileViewerMaxLineRunes {
			continue
		}
		truncated = true
		cut := 0
		for idx := range line {
			if cut == fileViewerMaxLineRunes {
				lines[i] = line[:idx] + " …[line truncated]"
				break
			}
			cut++
		}
	}
	if !truncated {
		return content, false
	}
	return strings.Join(lines, "\n"), true
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n >= div*unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ─── draw ──────────────────────────────────────────────────────────────────

func (v *fileViewer) draw(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if w < 40 || h < 8 {
		return
	}
	v.width = w
	v.height = h

	l, ok := drawSplitPane(scr, area, "file viewer", fileViewerNavW)
	if !ok {
		return
	}

	// ── context row: tab bar + close hint, matching the inspector ──
	drawPaneRow(scr, l.Context, palette.Muted.On(" "+v.tabBar()), palette.Subtle.On("ctrl+f close "))
	drawPaneHRule(scr, l.Context.Min.X, l.Context.Min.Y+1, l.Context.Dx(), -1, "")

	// ── nav panel ──
	switch v.mode {
	case fileViewerFiles:
		v.drawFileNav(scr, l)
	case fileViewerSkills:
		v.drawSkillNav(scr, l)
	case fileViewerAgents:
		v.drawAgentNav(scr, l)
	case fileViewerHooks:
		v.drawHookNav(scr, l)
	}

	// ── detail panel ──
	switch {
	case v.fileContent != "":
		v.drawContent(scr, l.Detail.Min.X, l.Detail.Min.Y, l.Detail.Dx(), l.Body.Max.Y)
	case v.mode == fileViewerFiles && v.cursor >= 0 && v.cursor < len(v.entries) && v.entries[v.cursor].IsDir():
		dirPath := filepath.Join(v.currentDir, v.entries[v.cursor].Name())
		v.drawDirPreview(scr, l.Detail.Min.X, l.Detail.Min.Y, l.Detail.Dx(), l.Detail.Dy(), dirPath)
	default:
		v.drawPlaceholder(scr, l.Detail.Min.X, l.Detail.Min.Y, l.Detail.Dx(), l.Detail.Dy())
	}

	// ── footer ──
	v.drawFooter(scr, l.Footer)
}

// tabBar renders the mode tabs in the shared overlay style (the inspector's):
// the active tab bracketed in the primary tone, the rest subtle.
func (v *fileViewer) tabBar() string {
	parts := make([]string, len(fileViewerModeNames))
	for i, name := range fileViewerModeNames {
		if fileViewerMode(i) == v.mode {
			parts[i] = palette.Primary.On("[ " + name + " ]")
		} else {
			parts[i] = palette.Subtle.On(name)
		}
	}
	return strings.Join(parts, "  ")
}

// drawNavStrip paints the per-mode summary row at the top of the nav rail and
// returns the list area below it. The context row belongs to the tab bar (as
// in the inspector), so mode-specific chrome lives inside the rail instead.
func drawNavStrip(scr uv.Screen, nav uv.Rectangle, text string) uv.Rectangle {
	if nav.Dx() < 1 || nav.Dy() < 1 {
		return nav
	}
	drawLine(scr, uv.Rect(nav.Min.X, nav.Min.Y, nav.Dx(), 1), ansi.Truncate(text, nav.Dx(), ""))
	return uv.Rect(nav.Min.X, nav.Min.Y+1, nav.Dx(), max(0, nav.Dy()-1))
}

func (v *fileViewer) drawFileNav(scr uv.Screen, l splitPaneLayout) {
	// ── path strip ──
	relPath, _ := filepath.Rel(v.workspaceDir, v.currentDir)
	if relPath == "." {
		relPath = "/"
	} else {
		relPath = "/" + relPath
	}
	nav := drawNavStrip(scr, l.Nav, palette.Muted.On(" "+relPath))

	// ── directory listing ──
	if len(v.entries) == 0 {
		drawLine(scr, uv.Rect(nav.Min.X, nav.Min.Y, nav.Dx(), 1), palette.Muted.On("  (empty)"))
	} else {
		start, end := windowAroundCursor(v.cursor, len(v.entries), nav.Dy())
		for i := start; i < end; i++ {
			e := v.entries[i]
			screenY := nav.Min.Y + (i - start)
			icon := fileIcon(e)
			drawListRow(scr, uv.Rect(nav.Min.X, screenY, nav.Dx(), 1), icon+" "+e.Name(), i == v.cursor, true)
		}
	}
}

// drawListNav renders a single-column name list (skills, agents, hooks)
// under a counted header. style colors both the header and each row's icon;
// nameAt yields the label for row i.
func (v *fileViewer) drawListNav(scr uv.Screen, l splitPaneLayout, style func(string) string, label, icon string, n int, nameAt func(int) string) {
	nav := drawNavStrip(scr, l.Nav, style(fmt.Sprintf(" %s (%d)", label, n)))

	if n == 0 {
		drawLine(scr, uv.Rect(nav.Min.X, nav.Min.Y, nav.Dx(), 1), palette.Muted.On("  (none)"))
		return
	}
	prefix := style(icon) + " "
	start, end := windowAroundCursor(v.cursor, n, nav.Dy())
	for i := start; i < end; i++ {
		screenY := nav.Min.Y + (i - start)
		drawListRow(scr, uv.Rect(nav.Min.X, screenY, nav.Dx(), 1), prefix+nameAt(i), i == v.cursor, true)
	}
}

func (v *fileViewer) drawSkillNav(scr uv.Screen, l splitPaneLayout) {
	v.drawListNav(scr, l, palette.PlanMode.On, "skills", "#", len(v.skills), func(i int) string { return v.skills[i].Name })
}

func (v *fileViewer) drawAgentNav(scr uv.Screen, l splitPaneLayout) {
	v.drawListNav(scr, l, palette.Info.On, "agents", "@", len(v.agents), func(i int) string { return v.agents[i].Name })
}

func (v *fileViewer) drawHookNav(scr uv.Screen, l splitPaneLayout) {
	v.drawListNav(scr, l, palette.Warning.On, "hooks", "!", len(v.hooks), func(i int) string { return v.hooks[i].Name })
}

func (v *fileViewer) drawFooter(scr uv.Screen, footer uv.Rectangle) {
	switch v.mode {
	case fileViewerSkills, fileViewerAgents, fileViewerHooks:
		f := keyLegend(
			keyHint{"↑↓/jk", "navigate"},
			keyHint{"enter/e", "edit source"},
			keyHint{"tab", "switch view"},
			keyHint{"pgup/pgdn", "scroll"},
			keyHint{"esc", "close"},
		)
		drawPaneRow(scr, footer, palette.Subtle.On(" "+f), "")
	default:
		f := keyLegend(
			keyHint{"↑↓/jk", "navigate"},
			keyHint{"enter", "open dir"},
			keyHint{"e", "edit file"},
			keyHint{"tab", "skills/agents/hooks"},
			keyHint{"backspace", "up"},
			keyHint{"pgup/pgdn", "scroll"},
			keyHint{"esc", "close"},
		)
		drawPaneRow(scr, footer, palette.Subtle.On(" "+f), "")
	}
}

func (v *fileViewer) drawContent(scr uv.Screen, x, y, w, footerY int) {
	// Header: file name, in the shared overlay header style.
	relPath, _ := filepath.Rel(v.workspaceDir, v.viewingFile)
	rawLineCount := len(strings.Split(v.fileContent, "\n"))
	drawLine(scr, uv.Rect(x, y, w, 1),
		headerLine(fmt.Sprintf("%s · %d lines", relPath, rawLineCount), w, palette.Info.On))
	y++

	cw := w - scrollbarWidth // reserve the gutter so content never underlaps it
	// Clamp scroll against the rendered code-block lines. These can differ from
	// raw source lines because syntax highlighting and wrapping happen before the
	// preview is drawn.
	contentLines := v.renderedContentLines(cw)
	contentH := footerY - y
	if contentH <= 0 {
		return
	}
	v.scroll = clampScrollOffset(v.scroll, len(contentLines), contentH)

	for i := v.scroll; i < len(contentLines) && (i-v.scroll) < contentH; i++ {
		screenY := y + (i - v.scroll)
		drawLine(scr, uv.Rect(x, screenY, cw, 1), contentLines[i])
	}
	drawPaneScrollbar(scr, x+w-1, y, contentH, len(contentLines), v.scroll)
}

// scrollLines scrolls the file preview by n lines (negative = up); the upper
// bound is clamped in drawContent against the live content height. Satisfies
// scroller for mouse-wheel routing.
func (v *fileViewer) scrollLines(n int) {
	v.scroll += n
	if v.scroll < 0 {
		v.scroll = 0
	}
}

func (v *fileViewer) renderedContentLines(width int) []string {
	relPath, _ := filepath.Rel(v.workspaceDir, v.viewingFile)
	return renderCodeBlock(width, v.fileContent, inferSyntaxFromHint(relPath),
		withBodyPrefix(" "),
		withLineNumbers(),
		withCacheKey("file-viewer:"+v.viewingFile),
	)
}

func (v *fileViewer) drawDirPreview(scr uv.Screen, x, y, w, bodyH int, dirPath string) {
	if v.dirPreview.path != dirPath {
		drawLine(scr, uv.Rect(x, y, w, 1), palette.Muted.On(" directory preview not loaded"))
		return
	}
	if v.dirPreview.err != "" {
		drawLine(scr, uv.Rect(x, y, w, 1), palette.Error.On(" could not read directory: "+v.dirPreview.err))
		return
	}
	relPath, _ := filepath.Rel(v.workspaceDir, dirPath)
	drawLine(scr, uv.Rect(x, y, w, 1),
		headerLine(fmt.Sprintf("%s/ · up to %d entries", relPath, fileViewerDirPreviewLimit), w, palette.Muted.On))
	for i, e := range v.dirPreview.entries {
		if i >= bodyH-1 {
			break
		}
		icon := filePreviewIcon(e)
		drawLine(scr, uv.Rect(x, y+1+i, w, 1), palette.Muted.On(fmt.Sprintf("   %s %s", icon, e.name)))
	}
	if v.dirPreview.truncated && len(v.dirPreview.entries) < bodyH-1 {
		drawLine(scr, uv.Rect(x, y+1+len(v.dirPreview.entries), w, 1), palette.Subtle.On("   … more entries"))
	}
}

func (v *fileViewer) drawPlaceholder(scr uv.Screen, x, y, w int, _ int) {
	drawLine(scr, uv.Rect(x, y, w, 1), palette.Muted.On(" select a file to preview"))
}

// ─── helpers ───────────────────────────────────────────────────────────────

func fileIcon(e os.DirEntry) string {
	return fileIconFor(e.Name(), e.IsDir())
}

func filePreviewIcon(e fileViewerPreviewEntry) string {
	return fileIconFor(e.name, e.isDir)
}

func fileIconFor(name string, isDir bool) string {
	if isDir {
		return palette.Info.On("/")
	}
	name = strings.ToLower(name)
	switch {
	case strings.HasSuffix(name, ".go"):
		return palette.Info.On("·")
	case strings.HasSuffix(name, ".md"):
		return palette.PlanMode.On("#")
	case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
		return palette.Warning.On(":")
	case strings.HasSuffix(name, ".json"):
		return palette.Warning.On("{")
	case strings.HasSuffix(name, ".proto"):
		return palette.Success.On(">")
	case strings.HasSuffix(name, ".sql"):
		return palette.Info.On(";")
	case strings.HasSuffix(name, ".sh"), strings.HasSuffix(name, ".bash"):
		return palette.Success.On("$")
	case strings.HasSuffix(name, ".mod"), strings.HasSuffix(name, ".sum"):
		return palette.Muted.On("·")
	case strings.HasSuffix(name, "dockerfile"), strings.HasSuffix(name, "docker-compose.yml"):
		return palette.Info.On("@")
	case strings.HasSuffix(name, ".env"), strings.HasSuffix(name, ".envrc"):
		return palette.Warning.On("*")
	case strings.HasSuffix(name, "makefile"):
		return palette.Muted.On(":")
	default:
		return palette.Subtle.On("·")
	}
}
