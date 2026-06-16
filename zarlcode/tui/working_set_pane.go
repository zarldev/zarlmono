package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

const workingSetNavW = 42

type workingSetView int

const (
	workingSetViewFiles workingSetView = iota
	workingSetViewTurns
	workingSetViewProcesses
)

// workingSetPane is the full-screen changed-files overlay. It also surfaces
// background processes managed by the live runner, mirroring the inspector's
// processes tab. File views remain read-only navigation/previews.
type workingSetPane struct {
	session       *Session
	live          *engine.LiveRunner
	workspaceDir  string
	view          workingSetView
	cursor        int
	processCursor int
	scroll        int
	height        int
	status        string
}

func newWorkingSetPane(session *Session, live *engine.LiveRunner, workspaceDir string) *workingSetPane {
	return &workingSetPane{session: session, live: live, workspaceDir: workspaceDir}
}

func (p *workingSetPane) fullScreen() bool { return true }

func (p *workingSetPane) handleKey(msg tea.KeyPressMsg) action {
	if p.view == workingSetViewProcesses {
		switch msg.String() {
		case "up", "k":
			if p.processCursor > 0 {
				p.processCursor--
			}
			return actionNone{}
		case "down", "j":
			if procs := p.processes(); p.processCursor < len(procs)-1 {
				p.processCursor++
			}
			return actionNone{}
		case "x", "delete", "backspace":
			proc, ok := p.selectedProcess()
			if !ok {
				p.status = "no process selected"
				return actionNone{}
			}
			if !proc.Running {
				p.status = "process already exited"
				return actionNone{}
			}
			p.status = "killing " + proc.ID + "…"
			return actionKillProcess{processID: proc.ID, signal: "TERM"}
		}
	}
	switch msg.String() {
	case "esc", "ctrl+w", "q":
		return actionClose{}
	case "tab":
		switch p.view {
		case workingSetViewFiles:
			p.view = workingSetViewTurns
		case workingSetViewTurns:
			p.view = workingSetViewProcesses
		default:
			p.view = workingSetViewFiles
		}
		p.cursor = 0
		p.processCursor = 0
		p.scroll = 0
		return actionNone{}
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
			p.scroll = 0
		}
	case "down", "j":
		if p.cursor < p.itemCount()-1 {
			p.cursor++
			p.scroll = 0
		}
	case "pgup":
		p.scroll -= max(1, p.height-4)
		if p.scroll < 0 {
			p.scroll = 0
		}
	case "pgdown":
		p.scroll += max(1, p.height-4)
	case "home", "g":
		p.cursor = 0
		p.scroll = 0
	case "end":
		if n := p.itemCount(); n > 0 {
			p.cursor = n - 1
		}
		p.scroll = 0
	case "enter":
		if p.session == nil || p.session.WorkingSet == nil {
			break
		}
		if p.view == workingSetViewTurns {
			if turn, ok := p.selectedTurn(); ok {
				return actionPush{d: newDiffBrowserForTurn(p.session.WorkingSet, p.session.Checkpoints, turn.ID)}
			}
			break
		}
		if file, ok := p.selectedFile(); ok {
			return actionPush{d: newDiffBrowserForFile(p.session.WorkingSet, p.session.Checkpoints, file.Path)}
		}
	case "r":
		if p.session == nil || p.session.Checkpoints == nil || p.session.WorkingSet == nil {
			break
		}
		if p.view == workingSetViewTurns {
			if turn, ok := p.selectedTurn(); ok {
				return actionPush{d: newRollbackDialogFor(p.session.Checkpoints, turn.ID, "")}
			}
			break
		}
		if file, ok := p.selectedFile(); ok {
			turnID, path := rollbackTargetFromMutations(p.session.WorkingSet.MutationsForFile(file.Path))
			if turnID != "" && path != "" {
				return actionPush{d: newRollbackDialogFor(p.session.Checkpoints, turnID, path)}
			}
		}
	case "o":
		if path := p.selectedPath(); path != "" {
			return actionPush{d: newFileViewerAt(p.workspaceDir, path)}
		}
	}
	return actionNone{}
}

func (p *workingSetPane) itemCount() int {
	switch p.view {
	case workingSetViewTurns:
		return len(p.turns())
	case workingSetViewFiles:
		return len(p.files())
	default:
		return len(p.processes())
	}
}

func (p *workingSetPane) files() []WorkingSetFile {
	if p == nil || p.session == nil || p.session.WorkingSet == nil {
		return nil
	}
	return p.session.WorkingSet.FilesChangedThisSession()
}

func (p *workingSetPane) turns() []WorkingSetTurn {
	if p == nil || p.session == nil || p.session.WorkingSet == nil {
		return nil
	}
	return p.session.WorkingSet.TurnsChangedThisSession()
}

func (p *workingSetPane) processes() []code.ProcessInfo {
	if p == nil || p.live == nil {
		return nil
	}
	return p.live.ProcessList()
}

func (p *workingSetPane) selectedProcess() (code.ProcessInfo, bool) {
	procs := p.processes()
	if len(procs) == 0 {
		return code.ProcessInfo{}, false
	}
	if p.processCursor < 0 {
		p.processCursor = 0
	}
	if p.processCursor >= len(procs) {
		p.processCursor = len(procs) - 1
	}
	return procs[p.processCursor], true
}

func (p *workingSetPane) selectedFile() (WorkingSetFile, bool) {
	files := p.files()
	if p.cursor < 0 || p.cursor >= len(files) {
		return WorkingSetFile{}, false
	}
	return files[p.cursor], true
}

func (p *workingSetPane) selectedTurn() (WorkingSetTurn, bool) {
	turns := p.turns()
	if p.cursor < 0 || p.cursor >= len(turns) {
		return WorkingSetTurn{}, false
	}
	return turns[p.cursor], true
}

func (p *workingSetPane) selectedPath() string {
	if p.view == workingSetViewFiles {
		if file, ok := p.selectedFile(); ok {
			return file.Path
		}
		return ""
	}
	if p.view == workingSetViewTurns {
		if mutation := p.selectedDiff(); mutation != nil {
			return mutation.Path
		}
	}
	return ""
}

func (p *workingSetPane) selectedDiff() *WorkingSetMutation {
	if p == nil || p.session == nil || p.session.WorkingSet == nil {
		return nil
	}
	if p.view == workingSetViewFiles {
		file, ok := p.selectedFile()
		if !ok {
			return nil
		}
		mutations := p.session.WorkingSet.MutationsForFile(file.Path)
		if len(mutations) == 0 {
			return nil
		}
		mutation := mutations[len(mutations)-1]
		return &mutation
	}
	if p.view == workingSetViewTurns {
		turn, ok := p.selectedTurn()
		if !ok {
			return nil
		}
		mutations := p.session.WorkingSet.MutationsForTurn(turn.ID)
		if len(mutations) == 0 {
			return nil
		}
		mutation := mutations[len(mutations)-1]
		return &mutation
	}
	return nil
}

func (p *workingSetPane) draw(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if w < 50 || h < 8 {
		return
	}
	l, ok := drawSplitPane(scr, area, p.titleLabel(), workingSetNavW)
	if !ok {
		return
	}
	p.height = l.Detail.Dy()
	p.clampCursor()

	drawPaneRow(scr, l.Context, palette.Muted.On(" "+p.contextText()), palette.Subtle.On("ctrl+w close "))
	p.drawNav(scr, l.Nav)
	p.drawDetail(scr, l.Detail)
	p.drawFooter(scr, l.Footer)
}

func (p *workingSetPane) clampCursor() {
	n := p.itemCount()
	if n == 0 {
		p.cursor = 0
		p.scroll = 0
		return
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= n {
		p.cursor = n - 1
	}
}

func (p *workingSetPane) contextText() string {
	files := p.files()
	turns := p.turns()
	procs := p.processes()
	running := 0
	for _, proc := range procs {
		if proc.Running {
			running++
		}
	}
	return fmt.Sprintf("%d files · %d turns · %d/%d processes", len(files), len(turns), running, len(procs))
}

func (p *workingSetPane) titleLabel() string {
	view := "files"
	switch p.view {
	case workingSetViewTurns:
		view = "turns"
	case workingSetViewProcesses:
		view = "processes"
	}
	return "working set · " + view
}

func (p *workingSetPane) drawNav(scr uv.Screen, r uv.Rectangle) {
	switch p.view {
	case workingSetViewTurns:
		p.drawTurnNav(scr, r)
		return
	case workingSetViewProcesses:
		p.drawProcessNav(scr, r)
		return
	}
	files := p.files()
	if len(files) == 0 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On("  no changed files this session"))
		return
	}
	start, end := windowAroundCursor(p.cursor, len(files), r.Dy())
	for i := start; i < end; i++ {
		file := files[i]
		screenY := r.Min.Y + (i - start)
		label := fmt.Sprintf("%s  +%d -%d  %s", file.Path, file.Additions, file.Deletions, countLabel(file.Mutations, "edit", "edits"))
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), label, i == p.cursor, true)
	}
}

func (p *workingSetPane) drawTurnNav(scr uv.Screen, r uv.Rectangle) {
	turns := p.turns()
	if len(turns) == 0 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On("  no turns with file edits"))
		return
	}
	start, end := windowAroundCursor(p.cursor, len(turns), r.Dy())
	for i := start; i < end; i++ {
		turn := turns[i]
		screenY := r.Min.Y + (i - start)
		label := fmt.Sprintf("turn #%d  %s  %s", turn.Ordinal, countLabel(turn.Files, "file", "files"), countLabel(turn.Mutations, "edit", "edits"))
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), label, i == p.cursor, true)
	}
}

func (p *workingSetPane) drawProcessNav(scr uv.Screen, r uv.Rectangle) {
	procs := p.processes()
	if len(procs) == 0 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On("  no background processes tracked"))
		return
	}
	start, end := windowAroundCursor(p.processCursor, len(procs), r.Dy())
	for i := start; i < end; i++ {
		proc := procs[i]
		screenY := r.Min.Y + (i - start)
		state := palette.Success.On("running")
		if !proc.Running {
			state = palette.Muted.On(fmt.Sprintf("exited %d", proc.ExitCode))
		}
		label := fmt.Sprintf("%s  pid=%d  %s  age=%s", palette.Info.On(proc.ID), proc.PID, state, time.Since(proc.StartedAt).Round(time.Second))
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), label, i == p.processCursor, true)
	}
}

func (p *workingSetPane) drawDetail(scr uv.Screen, r uv.Rectangle) {
	cw := r.Dx() - scrollbarWidth // reserve the gutter
	var lines []string
	switch p.view {
	case workingSetViewTurns:
		lines = p.turnDetailLines(cw)
	case workingSetViewProcesses:
		lines = p.processDetailLines(cw)
	default:
		lines = p.fileDetailLines(cw)
	}
	if len(lines) == 0 {
		msg := " no file mutations recorded yet"
		if p.view == workingSetViewProcesses {
			msg = " no background processes tracked"
		}
		lines = []string{palette.Muted.On(msg)}
	}
	p.scroll = clampScrollOffset(p.scroll, len(lines), r.Dy())
	for i := p.scroll; i < len(lines) && i-p.scroll < r.Dy(); i++ {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+i-p.scroll, cw, 1), ansi.Truncate(lines[i], cw, ""))
	}
	drawPaneScrollbar(scr, r.Max.X-1, r.Min.Y, r.Dy(), len(lines), p.scroll)
}

// scrollLines scrolls the working-set detail by n lines (negative = up); the
// upper bound is clamped in drawDetail. Satisfies scroller.
func (p *workingSetPane) scrollLines(n int) {
	p.scroll += n
	if p.scroll < 0 {
		p.scroll = 0
	}
}

func (p *workingSetPane) fileDetailLines(width int) []string {
	file, ok := p.selectedFile()
	if !ok || p.session == nil || p.session.WorkingSet == nil {
		return nil
	}
	mutations := p.session.WorkingSet.MutationsForFile(file.Path)
	lines := []string{
		headerLine(file.Path, width, palette.Primary.On),
		fmt.Sprintf(" status: %s", palette.Warning.On("mutated in session")),
		fmt.Sprintf(" summary: %s · %s · %s", countLabel(file.Mutations, "mutation", "mutations"), statBadge(file.Additions, file.Deletions), timeRange(file.FirstChangedAt, file.LastChangedAt)),
		"",
		palette.Subtle.On(" turns"),
	}
	seen := make(map[string]struct{})
	for _, mutation := range mutations {
		label := turnLabel(mutation)
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		lines = append(lines, "  "+label)
	}
	lines = append(lines, "", palette.Subtle.On(" latest mutation"))
	if len(mutations) > 0 {
		latest := mutations[len(mutations)-1]
		lines = append(lines,
			fmt.Sprintf("  mutation #%d · %s · %s", latest.MutationOrdinal, turnLabel(latest), statBadge(latest.Additions, latest.Deletions)),
			"",
			palette.Subtle.On(" latest diff"),
		)
		lines = append(lines, renderContentBlock(width, contentBlock{kind: contentDiff, text: latest.Diff, bodyPrefix: "  ", maxLines: max(1, width/3)})...)
	}
	return lines
}

func (p *workingSetPane) turnDetailLines(width int) []string {
	turn, ok := p.selectedTurn()
	if !ok || p.session == nil || p.session.WorkingSet == nil {
		return nil
	}
	files := p.session.WorkingSet.FilesChangedForTurn(turn.ID)
	mutations := p.session.WorkingSet.MutationsForTurn(turn.ID)
	lines := []string{
		headerLine(fmt.Sprintf("turn #%d", turn.Ordinal), width, palette.Primary.On),
		fmt.Sprintf(" id: %s", turn.ID),
		fmt.Sprintf(" summary: %s · %s · %s · %s", countLabel(turn.Files, "file", "files"), countLabel(turn.Mutations, "mutation", "mutations"), statBadge(turn.Additions, turn.Deletions), timeRange(turn.FirstChangedAt, turn.LastChangedAt)),
		"",
		palette.Subtle.On(" files"),
	}
	for _, file := range files {
		lines = append(lines, fmt.Sprintf("  %s · %s · %s", file.Path, countLabel(file.Mutations, "mutation", "mutations"), statBadge(file.Additions, file.Deletions)))
	}
	lines = append(lines, "", palette.Subtle.On(" latest diff"))
	if len(mutations) > 0 {
		latest := mutations[len(mutations)-1]
		lines = append(lines, fmt.Sprintf("  %s · mutation #%d · %s", latest.Path, latest.MutationOrdinal, statBadge(latest.Additions, latest.Deletions)))
		lines = append(lines, renderContentBlock(width, contentBlock{kind: contentDiff, text: latest.Diff, bodyPrefix: "  ", maxLines: max(1, width/3)})...)
	}
	return lines
}

func (p *workingSetPane) processDetailLines(width int) []string {
	proc, ok := p.selectedProcess()
	if !ok {
		return nil
	}
	state := palette.Success.On("running")
	if !proc.Running {
		state = palette.Muted.On(fmt.Sprintf("exited %d", proc.ExitCode))
	}
	lines := []string{
		headerLine(proc.ID, width, palette.Primary.On),
		fmt.Sprintf(" pid: %d", proc.PID),
		fmt.Sprintf(" state: %s", state),
		fmt.Sprintf(" cwd: %s", palette.Muted.On(proc.CWD)),
		fmt.Sprintf(" command: %s", palette.Muted.On(proc.Command)),
		fmt.Sprintf(" started: %s", proc.StartedAt.Format("15:04:05")),
		fmt.Sprintf(" age: %s", time.Since(proc.StartedAt).Round(time.Second)),
		fmt.Sprintf(" stdout: %d lines", proc.StdoutLines),
		fmt.Sprintf(" stderr: %d lines", proc.StderrLines),
	}
	if p.status != "" {
		lines = append(lines, "", palette.Muted.On(p.status))
	}
	return lines
}

func (p *workingSetPane) drawFooter(scr uv.Screen, r uv.Rectangle) {
	hints := []keyHint{{"↑↓/jk", "select"}, {"tab", "files/turns/processes"}, {"enter", "diff"}, {"o", "open file"}, {"r", "rollback"}, {"esc", "close"}}
	if p.view == workingSetViewProcesses {
		hints = []keyHint{{"↑↓/jk", "select process"}, {"x", "kill"}, {"tab", "files/turns/processes"}, {"esc", "close"}}
	}
	footer := keyLegend(hints...)
	drawPaneRow(scr, r, palette.Subtle.On(" "+footer), "")
}

func headerLine(label string, width int, style func(string) string) string {
	head := " " + label + " "
	return style(head) + palette.Subtle.On(strings.Repeat("─", max(0, width-ansi.StringWidth(head))))
}

func statBadge(add, del int) string {
	return palette.Success.On("+"+strconv.Itoa(add)) + " " + palette.Error.On("-"+strconv.Itoa(del))
}

func turnLabel(mutation WorkingSetMutation) string {
	if mutation.TurnID == "" {
		return "outside turn"
	}
	return fmt.Sprintf("turn #%d · %s", mutation.TurnOrdinal, mutation.TurnID)
}

func timeRange(first, last time.Time) string {
	if first.IsZero() && last.IsZero() {
		return "no timestamp"
	}
	if first.Equal(last) {
		return first.Format("15:04:05")
	}
	return first.Format("15:04:05") + "–" + last.Format("15:04:05")
}

func countLabel(n int, singular, countLabel string) string {
	word := countLabel
	if n == 1 {
		word = singular
	}
	return fmt.Sprintf("%d %s", n, word)
}
