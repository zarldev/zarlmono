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

	left := overlayTopBar(p.titleLabel(), p.tabNames(), p.activeTab(), p.contextText(), l.Context.Dx())
	drawOverlayContext(scr, l, left, palette.Subtle.On("ctrl+w close "), palette.Border)
	p.drawNav(scr, p.drawSectionChrome(scr, l.Nav, p.navSummary()))
	p.drawDetail(scr, p.drawSectionChrome(scr, l.Detail, p.viewSummary()))
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

func (p *workingSetPane) titleLabel() string { return "working set" }

func (p *workingSetPane) tabNames() []string { return []string{"files", "turns", "processes"} }

func (p *workingSetPane) activeTab() int { return int(p.view) }

func (p *workingSetPane) viewSummary() string {
	switch p.view {
	case workingSetViewTurns:
		return fmt.Sprintf("%d turns", len(p.turns()))
	case workingSetViewProcesses:
		procs := p.processes()
		running := 0
		for _, proc := range procs {
			if proc.Running {
				running++
			}
		}
		return fmt.Sprintf("%d/%d running", running, len(procs))
	default:
		return fmt.Sprintf("%d files", len(p.files()))
	}
}

func (p *workingSetPane) navSummary() string {
	switch p.view {
	case workingSetViewTurns:
		if turn, ok := p.selectedTurn(); ok {
			return fmt.Sprintf("turn focus · #%d · %s", turn.Ordinal, countLabel(turn.Files, "file", "files"))
		}
		return "turns changed this session"
	case workingSetViewProcesses:
		if proc, ok := p.selectedProcess(); ok {
			state := "running"
			if !proc.Running {
				state = fmt.Sprintf("exited %d", proc.ExitCode)
			}
			return fmt.Sprintf("process focus · %s · pid %d", state, proc.PID)
		}
		return "background processes for this session"
	default:
		if file, ok := p.selectedFile(); ok {
			return fmt.Sprintf("file focus · %s · %s", statBadge(file.Additions, file.Deletions), countLabel(file.Mutations, "mutation", "mutations"))
		}
		return "files changed this session"
	}
}

func (p *workingSetPane) drawSectionChrome(scr uv.Screen, r uv.Rectangle, summary string) uv.Rectangle {
	if r.Dy() <= 0 {
		return r
	}
	drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), ansi.Truncate(palette.Muted.On(" "+summary), r.Dx(), ""))
	if r.Dy() > 1 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+1, r.Dx(), 1), palette.Border.On(strings.Repeat("─", r.Dx())))
	}
	return uv.Rect(r.Min.X, r.Min.Y+2, r.Dx(), max(0, r.Dy()-2))
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
	start, end := windowAroundCursor(p.cursor, len(files), max(1, r.Dy()/2))
	for i := start; i < end; i++ {
		file := files[i]
		screenY := r.Min.Y + (i-start)*2
		if screenY >= r.Max.Y {
			break
		}
		primary := fmt.Sprintf("%s  %s", file.Path, statBadge(file.Additions, file.Deletions))
		secondary := palette.Subtle.On("    " + countLabel(file.Mutations, "mutation", "mutations") + " · " + timeRange(file.FirstChangedAt, file.LastChangedAt))
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), primary, i == p.cursor, true)
		if screenY+1 < r.Max.Y {
			drawLine(scr, uv.Rect(r.Min.X, screenY+1, r.Dx(), 1), ansi.Truncate(secondary, r.Dx(), ""))
		}
	}
}

func (p *workingSetPane) drawTurnNav(scr uv.Screen, r uv.Rectangle) {
	turns := p.turns()
	if len(turns) == 0 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On("  no turns with file edits"))
		return
	}
	start, end := windowAroundCursor(p.cursor, len(turns), max(1, r.Dy()/2))
	for i := start; i < end; i++ {
		turn := turns[i]
		screenY := r.Min.Y + (i-start)*2
		if screenY >= r.Max.Y {
			break
		}
		primary := fmt.Sprintf("turn #%d  %s", turn.Ordinal, statBadge(turn.Additions, turn.Deletions))
		secondary := palette.Subtle.On("    " + countLabel(turn.Files, "file", "files") + " · " + countLabel(turn.Mutations, "edit", "edits") + " · " + turn.ID)
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), primary, i == p.cursor, true)
		if screenY+1 < r.Max.Y {
			drawLine(scr, uv.Rect(r.Min.X, screenY+1, r.Dx(), 1), ansi.Truncate(secondary, r.Dx(), ""))
		}
	}
}

func (p *workingSetPane) drawProcessNav(scr uv.Screen, r uv.Rectangle) {
	procs := p.processes()
	if len(procs) == 0 {
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), palette.Muted.On("  no background processes tracked"))
		return
	}
	start, end := windowAroundCursor(p.processCursor, len(procs), max(1, r.Dy()/2))
	for i := start; i < end; i++ {
		proc := procs[i]
		screenY := r.Min.Y + (i-start)*2
		if screenY >= r.Max.Y {
			break
		}
		state := palette.Success.On("running")
		if !proc.Running {
			state = palette.Muted.On(fmt.Sprintf("exited %d", proc.ExitCode))
		}
		primary := fmt.Sprintf("%s  pid=%d  %s", palette.Info.On(proc.ID), proc.PID, state)
		secondary := palette.Subtle.On(fmt.Sprintf("    age %s · stdout %d · stderr %d", time.Since(proc.StartedAt).Round(time.Second), proc.StdoutLines, proc.StderrLines))
		drawListRow(scr, uv.Rect(r.Min.X, screenY, r.Dx(), 1), primary, i == p.processCursor, true)
		if screenY+1 < r.Max.Y {
			drawLine(scr, uv.Rect(r.Min.X, screenY+1, r.Dx(), 1), ansi.Truncate(secondary, r.Dx(), ""))
		}
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
		return []string{
			headerLine("file detail", width, palette.Primary.On),
			palette.Muted.On(" status: unavailable"),
			palette.Subtle.On(" choose a changed file to inspect its turn history and latest diff"),
		}
	}
	mutations := p.session.WorkingSet.MutationsForFile(file.Path)
	lines := []string{
		headerLine(file.Path, width, palette.Primary.On),
		fmt.Sprintf(" status: %s", palette.Warning.On("mutated in session")),
		fmt.Sprintf(" source: %s", timeRange(file.FirstChangedAt, file.LastChangedAt)),
		fmt.Sprintf(" summary: %s · %s", countLabel(file.Mutations, "mutation", "mutations"), statBadge(file.Additions, file.Deletions)),
		fmt.Sprintf(" actions: %s · %s · %s", palette.Info.On("enter diff"), palette.Info.On("o open file"), palette.Info.On("r rollback")),
		"",
		sectionHead("turns", width),
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
	if len(mutations) == 0 {
		lines = append(lines, palette.Muted.On("  no recorded mutations for this file yet"))
	}
	lines = append(lines, "", sectionHead("latest mutation", width))
	if len(mutations) > 0 {
		latest := mutations[len(mutations)-1]
		lines = append(lines,
			fmt.Sprintf("  mutation #%d · %s · %s", latest.MutationOrdinal, turnLabel(latest), statBadge(latest.Additions, latest.Deletions)),
			"",
			sectionHead("latest diff", width),
		)
		lines = append(lines, renderContentBlock(width, contentBlock{kind: contentDiff, text: latest.Diff, bodyPrefix: "  ", maxLines: max(1, width/3)})...)
	} else {
		lines = append(lines, palette.Muted.On("  no diff captured yet"))
	}
	return lines
}

func (p *workingSetPane) turnDetailLines(width int) []string {
	turn, ok := p.selectedTurn()
	if !ok || p.session == nil || p.session.WorkingSet == nil {
		return []string{
			headerLine("turn detail", width, palette.Primary.On),
			palette.Muted.On(" status: unavailable"),
			palette.Subtle.On(" choose a turn to inspect changed files and the latest diff"),
		}
	}
	files := p.session.WorkingSet.FilesChangedForTurn(turn.ID)
	mutations := p.session.WorkingSet.MutationsForTurn(turn.ID)
	lines := []string{
		headerLine(fmt.Sprintf("turn #%d", turn.Ordinal), width, palette.Primary.On),
		fmt.Sprintf(" status: %s", palette.Warning.On("mutated in session")),
		fmt.Sprintf(" id: %s", turn.ID),
		fmt.Sprintf(" summary: %s · %s · %s", countLabel(turn.Files, "file", "files"), countLabel(turn.Mutations, "mutation", "mutations"), statBadge(turn.Additions, turn.Deletions)),
		fmt.Sprintf(" actions: %s · %s", palette.Info.On("enter diff"), palette.Info.On("r rollback turn")),
		"",
		sectionHead("files", width),
	}
	for _, file := range files {
		lines = append(lines, fmt.Sprintf("  %s · %s · %s", file.Path, countLabel(file.Mutations, "mutation", "mutations"), statBadge(file.Additions, file.Deletions)))
	}
	if len(files) == 0 {
		lines = append(lines, palette.Muted.On("  no files recorded for this turn"))
	}
	lines = append(lines, "", sectionHead("latest diff", width))
	if len(mutations) > 0 {
		latest := mutations[len(mutations)-1]
		lines = append(lines, fmt.Sprintf("  %s · mutation #%d · %s", latest.Path, latest.MutationOrdinal, statBadge(latest.Additions, latest.Deletions)))
		lines = append(lines, renderContentBlock(width, contentBlock{kind: contentDiff, text: latest.Diff, bodyPrefix: "  ", maxLines: max(1, width/3)})...)
	} else {
		lines = append(lines, palette.Muted.On("  no diff captured yet"))
	}
	return lines
}

func (p *workingSetPane) processDetailLines(width int) []string {
	proc, ok := p.selectedProcess()
	if !ok {
		return []string{
			headerLine("process detail", width, palette.Primary.On),
			palette.Muted.On(" status: unavailable"),
			palette.Subtle.On(" choose a tracked process to inspect its state and output counters"),
		}
	}
	state := palette.Success.On("running")
	if !proc.Running {
		state = palette.Muted.On(fmt.Sprintf("exited %d", proc.ExitCode))
	}
	lines := []string{
		headerLine(proc.ID, width, palette.Primary.On),
		fmt.Sprintf(" status: %s", state),
		fmt.Sprintf(" source: pid %d · %s", proc.PID, proc.StartedAt.Format("15:04:05")),
		fmt.Sprintf(" path: %s", palette.Muted.On(proc.CWD)),
		fmt.Sprintf(" command: %s", palette.Muted.On(proc.Command)),
		fmt.Sprintf(" summary: age %s · stdout %d · stderr %d", time.Since(proc.StartedAt).Round(time.Second), proc.StdoutLines, proc.StderrLines),
		fmt.Sprintf(" actions: %s", palette.Info.On("x kill process")),
	}
	if p.status != "" {
		lines = append(lines, "", palette.Muted.On(p.status))
	}
	return lines
}

func (p *workingSetPane) drawFooter(scr uv.Screen, r uv.Rectangle) {
	hints := []keyHint{{"↑↓/jk", "navigate"}, {"tab", "switch view"}, {"enter", "show diff"}, {"o", "open file"}, {"r", "rollback"}, {"pgup/pgdn", "scroll detail"}, {"esc", "close"}}
	if p.view == workingSetViewProcesses {
		hints = []keyHint{{"↑↓/jk", "navigate"}, {"x", "kill process"}, {"tab", "switch view"}, {"pgup/pgdn", "scroll detail"}, {"esc", "close"}}
	}
	drawPaneRow(scr, r, palette.Subtle.On(" "+compactFooterHints(hints...)), "")
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
