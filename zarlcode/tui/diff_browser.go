package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const diffBrowserNavW = 44

type diffBrowserMode int

const (
	diffBrowserByTurn diffBrowserMode = iota
	diffBrowserByFile
	diffBrowserSession
)

type diffBrowser struct {
	workingSet  *WorkingSet
	checkpoints *Checkpoints
	mode        diffBrowserMode
	cursor      int
	scroll      int
	height      int
}

type diffBrowserEntry struct {
	label     string
	subtitle  string
	matchKey  string
	mutations []WorkingSetMutation
}

func newDiffBrowser(ws *WorkingSet) *diffBrowser {
	return &diffBrowser{workingSet: ws}
}

func newDiffBrowserForFile(ws *WorkingSet, checkpoints *Checkpoints, path string) *diffBrowser {
	d := &diffBrowser{workingSet: ws, checkpoints: checkpoints, mode: diffBrowserByFile}
	d.selectMatch(path)
	return d
}

func newDiffBrowserForTurn(ws *WorkingSet, checkpoints *Checkpoints, turnID string) *diffBrowser {
	d := &diffBrowser{workingSet: ws, checkpoints: checkpoints, mode: diffBrowserByTurn}
	d.selectMatch(turnID)
	return d
}

func (d *diffBrowser) fullScreen() bool { return true }

func (d *diffBrowser) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "q", "enter":
		return actionClose{}
	case "tab":
		d.nextMode()
	case "up", "k", "p":
		if d.cursor > 0 {
			d.cursor--
			d.scroll = 0
		}
	case "down", "j", "n":
		if d.cursor < len(d.entries())-1 {
			d.cursor++
			d.scroll = 0
		}
	case "pgup":
		d.scroll -= max(1, d.height-4)
	case "pgdown":
		d.scroll += max(1, d.height-4)
	case "home", "g":
		d.cursor = 0
		d.scroll = 0
	case "end":
		if n := len(d.entries()); n > 0 {
			d.cursor = n - 1
		}
		d.scroll = 0
	case "r":
		if d.checkpoints != nil {
			turnID, path := d.rollbackTarget()
			if turnID != "" {
				return actionPush{d: newRollbackDialogFor(d.checkpoints, turnID, path)}
			}
		}
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
	return actionNone{}
}

func (d *diffBrowser) rollbackTarget() (string, string) {
	entry, ok := d.selectedEntry()
	if !ok {
		return "", ""
	}
	if d.mode == diffBrowserByTurn {
		return entry.matchKey, ""
	}
	return rollbackTargetFromMutations(entry.mutations)
}

func (d *diffBrowser) nextMode() {
	switch d.mode {
	case diffBrowserByTurn:
		d.mode = diffBrowserByFile
	case diffBrowserByFile:
		d.mode = diffBrowserSession
	default:
		d.mode = diffBrowserByTurn
	}
	d.cursor = 0
	d.scroll = 0
}

func (d *diffBrowser) selectMatch(key string) {
	if key == "" {
		return
	}
	for i, entry := range d.entries() {
		if entry.matchKey == key {
			d.cursor = i
			return
		}
	}
}

func (d *diffBrowser) entries() []diffBrowserEntry {
	if d == nil || d.workingSet == nil {
		return nil
	}
	switch d.mode {
	case diffBrowserByFile:
		return d.fileEntries()
	case diffBrowserSession:
		return d.sessionEntries()
	default:
		return d.turnEntries()
	}
}

func (d *diffBrowser) turnEntries() []diffBrowserEntry {
	turns := d.workingSet.TurnsChangedThisSession()
	entries := make([]diffBrowserEntry, 0, len(turns))
	for _, turn := range turns {
		mutations := d.workingSet.MutationsForTurn(turn.ID)
		entries = append(entries, diffBrowserEntry{
			label:     fmt.Sprintf("turn #%d", turn.Ordinal),
			subtitle:  fmt.Sprintf("%s · %s · %s", countLabel(turn.Files, "file", "files"), countLabel(turn.Mutations, "diff", "diffs"), statBadge(turn.Additions, turn.Deletions)),
			matchKey:  turn.ID,
			mutations: mutations,
		})
	}
	return entries
}

func (d *diffBrowser) fileEntries() []diffBrowserEntry {
	files := d.workingSet.FilesChangedThisSession()
	entries := make([]diffBrowserEntry, 0, len(files))
	for _, file := range files {
		mutations := d.workingSet.MutationsForFile(file.Path)
		entries = append(entries, diffBrowserEntry{
			label:     file.Path,
			subtitle:  fmt.Sprintf("%s · %s", countLabel(file.Mutations, "diff", "diffs"), statBadge(file.Additions, file.Deletions)),
			matchKey:  file.Path,
			mutations: mutations,
		})
	}
	return entries
}

func (d *diffBrowser) sessionEntries() []diffBrowserEntry {
	mutations := d.workingSet.MutationsThisSession()
	if len(mutations) == 0 {
		return nil
	}
	add, del := 0, 0
	files := make(map[string]struct{})
	for _, mutation := range mutations {
		add += mutation.Additions
		del += mutation.Deletions
		files[mutation.Path] = struct{}{}
	}
	return []diffBrowserEntry{{
		label:     "session patch",
		subtitle:  fmt.Sprintf("%s · %s · %s", countLabel(len(files), "file", "files"), countLabel(len(mutations), "diff", "diffs"), statBadge(add, del)),
		matchKey:  "session",
		mutations: mutations,
	}}
}

func (d *diffBrowser) selectedEntry() (diffBrowserEntry, bool) {
	entries := d.entries()
	if d.cursor < 0 || d.cursor >= len(entries) {
		return diffBrowserEntry{}, false
	}
	return entries[d.cursor], true
}

func (d *diffBrowser) draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() < 50 || area.Dy() < 8 {
		return
	}
	l, ok := drawSplitPane(scr, area, d.titleLabel(), diffBrowserNavW)
	if !ok {
		return
	}
	d.height = l.Detail.Dy()
	d.clampCursor()
	drawPaneRow(scr, l.Context, palette.Muted.On(" "+d.contextText()), palette.Subtle.On("enter back "))
	d.drawNav(scr, l.Nav)
	d.drawDetail(scr, l.Detail)
	d.drawFooter(scr, l.Footer)
}

func (d *diffBrowser) clampCursor() {
	n := len(d.entries())
	if n == 0 {
		d.cursor = 0
		d.scroll = 0
		return
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
	if d.cursor >= n {
		d.cursor = n - 1
	}
}

func (d *diffBrowser) contextText() string {
	return countLabel(len(d.entries()), "group", "groups")
}

func (d *diffBrowser) titleLabel() string { return "diff browser · " + d.modeLabel() }

func (d *diffBrowser) modeLabel() string {
	switch d.mode {
	case diffBrowserByFile:
		return "by file"
	case diffBrowserSession:
		return "session patch"
	default:
		return "by turn"
	}
}

func (d *diffBrowser) drawNav(scr uv.Screen, r uv.Rectangle) {
	entries := d.entries()
	if len(entries) == 0 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On("  no diffs recorded"))
		return
	}
	start, end := windowAroundCursor(d.cursor, len(entries), r.Dy())
	for i := start; i < end; i++ {
		entry := entries[i]
		screenY := r.Min.Y + (i - start)
		label := entry.label
		if entry.subtitle != "" {
			label += "  " + ansi.Strip(entry.subtitle)
		}
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), label, i == d.cursor, true)
	}
}

func (d *diffBrowser) drawDetail(scr uv.Screen, r uv.Rectangle) {
	entry, ok := d.selectedEntry()
	if !ok {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On(" no diffs recorded yet"))
		return
	}
	cw := r.Dx() - scrollbarWidth // reserve the gutter
	lines := []string{
		headerLine(entry.label, cw, palette.Primary.On),
		" " + entry.subtitle,
		fmt.Sprintf(" %s", countLabel(len(entry.mutations), "captured diff", "captured diffs")),
		"",
	}
	lines = append(lines, renderContentBlock(cw, contentBlock{kind: contentDiff, text: browserPatch(entry.mutations), bodyPrefix: " "})...)
	d.scroll = clampScrollOffset(d.scroll, len(lines), r.Dy())
	for i := d.scroll; i < len(lines) && i-d.scroll < r.Dy(); i++ {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+i-d.scroll, cw, 1), ansi.Truncate(lines[i], cw, ""))
	}
	drawPaneScrollbar(scr, r.Max.X-1, r.Min.Y, r.Dy(), len(lines), d.scroll)
}

// scrollLines scrolls the diff detail by n lines (negative = up); the upper
// bound is clamped in drawDetail. Satisfies scroller for mouse-wheel routing.
func (d *diffBrowser) scrollLines(n int) {
	d.scroll += n
	if d.scroll < 0 {
		d.scroll = 0
	}
}

func (d *diffBrowser) drawFooter(scr uv.Screen, r uv.Rectangle) {
	footer := keyLegend(
		keyHint{"↑↓/jk", "group"},
		keyHint{"tab", "mode"},
		keyHint{"pgup/pgdn", "scroll"},
		keyHint{"r", "rollback"},
		keyHint{"n/p", "next/prev"},
		keyHint{"esc", "back"},
	)
	drawPaneRow(scr, r, palette.Subtle.On(" "+footer), "")
}

func browserPatch(mutations []WorkingSetMutation) string {
	if len(mutations) == 0 {
		return ""
	}
	parts := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		header := fmt.Sprintf("# %s · mutation #%d · %s", mutation.Path, mutation.MutationOrdinal, turnLabel(mutation))
		parts = append(parts, header+"\n"+strings.TrimRight(mutation.Diff, "\n"))
	}
	return strings.Join(parts, "\n\n")
}
