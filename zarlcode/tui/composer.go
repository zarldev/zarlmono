package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// composer is the editor-pane text input. It is a plain rune buffer with
// a cursor — the root model routes key events to it; it does not
// implement tea.Model.
type composer struct {
	value  []rune
	cursor int
}

func (c *composer) insert(s string) {
	rs := []rune(s)
	out := make([]rune, 0, len(c.value)+len(rs))
	out = append(out, c.value[:c.cursor]...)
	out = append(out, rs...)
	out = append(out, c.value[c.cursor:]...)
	c.value = out
	c.cursor += len(rs)
}

func (c *composer) backspace() {
	if c.cursor == 0 {
		return
	}
	c.value = append(c.value[:c.cursor-1], c.value[c.cursor:]...)
	c.cursor--
}

func (c *composer) left() {
	if c.cursor > 0 {
		c.cursor--
	}
}

func (c *composer) right() {
	if c.cursor < len(c.value) {
		c.cursor++
	}
}

func (c *composer) text() string { return string(c.value) }

func (c *composer) setText(s string) {
	c.value = []rune(s)
	c.cursor = len(c.value)
}

// submit returns the trimmed buffer and clears it.
func (c *composer) submit() string {
	v := strings.TrimSpace(string(c.value))
	c.value = nil
	c.cursor = 0
	return v
}

func (c *composer) displayLines(width int) []string {
	innerW := width - 2
	// Reserve the 2-column prompt prefix ("› " / "  ") that every wrapped
	// line is rendered with below, so a filled line isn't clipped when the
	// prefix is prepended and the whole thing is padded back to innerW.
	wrapW := innerW - 2
	if wrapW < 1 {
		wrapW = 1
	}

	// Build a plain-text display string with an unstyled cursor marker, wrap it,
	// then style the cursor and prefix after wrapping so ANSI codes don't throw
	// off lipgloss width measurement.
	display := string(c.value[:c.cursor]) + "▏" + string(c.value[c.cursor:])
	lines := wrapText(display, wrapW)
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (c *composer) displayLineCount(width int) int {
	return len(c.displayLines(width))
}

func (c *composer) draw(scr uv.Screen, r uv.Rectangle, planMode bool) {
	// In PLAN mode the frame, label, and prompt glyph take the theme's
	// PlanMode tint so the read-only mode is unmistakable.
	border, accent, label := palette.Border, palette.Primary, "editor"
	if planMode {
		border, accent, label = palette.PlanMode, palette.PlanMode, "plan"
	}
	drawBoxColored(scr, r, label, border, accent)
	w, h := r.Dx(), r.Dy()
	if w < 4 || h < 3 {
		return
	}
	innerW := w - 2
	maxLines := h - 2

	lines := c.displayLines(w)

	// Find cursor display line for scroll tracking.
	cursorLine := -1
	for i, line := range lines {
		if strings.ContainsRune(line, '▏') {
			cursorLine = i
			break
		}
	}

	// Style the cursor marker now that wrapping is settled.
	for i := range lines {
		lines[i] = strings.ReplaceAll(lines[i], "▏", accent.On("▏"))
	}

	// Scroll: keep the cursor line visible within the max-lines viewport.
	if cursorLine >= maxLines {
		start := cursorLine - maxLines + 1
		lines = lines[start:min(start+maxLines, len(lines))]
	} else if len(lines) > maxLines {
		lines = lines[:maxLines]
	}

	for i, line := range lines {
		if i >= maxLines {
			break
		}
		prefix := "  "
		if i == 0 {
			prefix = accent.On("›") + " "
		}
		uv.NewStyledString(padStyled(prefix+line, innerW)).
			Draw(scr, uv.Rect(r.Min.X+1, r.Min.Y+1+i, innerW, 1))
	}
}

// handleKey routes a key press to the active shell surface. Dialogs and
// global shortcuts are handled first; focused surfaces get small dedicated
// handlers so the root routing stays readable.
func (m *UI) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.overlay.active() {
		return m.handleAction(m.overlay.top().handleKey(msg))
	}
	switch msg.String() {
	case "ctrl+c":
		return m.handleQuit()
	case "ctrl+q":
		m.overlay.push(newClearContextConfirmDialog())
		return nil
	}
	if cmd, ok := m.handleGlobalShortcut(msg); ok {
		return cmd
	}
	if m.intro != nil {
		return m.handleIntroKey(msg)
	}
	if m.session.CockpitExpanded {
		return m.handleDashboardKey(msg)
	}
	if m.timeline.browsing {
		return m.handleBrowseKey(msg)
	}
	return m.handleComposerKey(msg)
}

func (m *UI) handleGlobalShortcut(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+w":
		m.overlay.push(newWorkingSetPane(m.session, m.live, m.session.WorkspaceDir))
		return nil, true
	case "ctrl+y":
		if m.live != nil {
			m.overlay.push(newSteerTray(m.live))
		}
		return nil, true
	case "ctrl+o":
		m.overlay.push(newInspector(BuildInspectorSnapshot(m.session, m.live, nil)))
		return nil, true
	}
	return nil, false
}

func (m *UI) handleCommonShortcut(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+f":
		m.overlay.push(newFileViewer(m.session.WorkspaceDir))
		return nil, true
	case "ctrl+e":
		return m.openModelQuickPick(), true
	case "ctrl+k":
		if m.settings != nil {
			m.overlay.push(newCatalogDialog(m.settings))
		}
		return nil, true
	case "ctrl+g":
		m.overlay.push(newHelpDialog())
		return nil, true
	case "ctrl+t":
		m.overlay.push(newThemePicker())
		return nil, true
	case "ctrl+s":
		if m.settings != nil {
			return m.openSettings(), true
		}
		return nil, true
	case "ctrl+p":
		m.overlay.push(newPlanDialog(&m.session.Plan, m.session.WorkspaceDir))
		return nil, true
	case "shift+tab":
		m.togglePlan()
		return nil, true
	}
	return nil, false
}

func (m *UI) handleIntroKey(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.handleCommonShortcut(msg); ok {
		return cmd
	}
	return m.intro.handleKey(m, msg)
}

func (m *UI) handleDashboardKey(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.handleDashboardShortcut(msg); ok {
		return cmd
	}
	switch msg.String() {
	case "up", "k":
		m.dashboardScroll--
		m.clampDashboardScroll()
	case "down", "j":
		m.dashboardScroll++
		m.clampDashboardScroll()
	case "pgup":
		m.dashboardScroll -= m.dashboardPageStep()
		m.clampDashboardScroll()
	case "pgdown":
		m.dashboardScroll += m.dashboardPageStep()
		m.clampDashboardScroll()
	case "home", "g":
		m.dashboardScroll = 0
	case "end":
		m.dashboardScroll = m.dashboardMaxScroll()
	case "esc", "ctrl+l", "q":
		m.session.SetCockpitExpanded(false)
	}
	return nil
}

func (m *UI) handleDashboardShortcut(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+f":
		m.overlay.push(newFileViewer(m.session.WorkspaceDir))
		return nil, true
	case "ctrl+e":
		return m.openModelQuickPick(), true
	case "ctrl+k":
		if m.settings != nil {
			m.overlay.push(newCatalogDialog(m.settings))
		}
		return nil, true
	}
	return nil, false
}

func (m *UI) handleBrowseKey(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.handleDashboardShortcut(msg); ok {
		return cmd
	}
	switch msg.String() {
	case "esc", "i":
		m.timeline.exitBrowse()
	case "up", "k":
		m.timeline.cursorUp()
	case "down", "j":
		m.timeline.cursorDown()
	case "g", "home":
		m.timeline.cursorTop()
	case "end":
		m.timeline.cursorBottom()
	case "pgup":
		m.timeline.pageUp()
	case "pgdown":
		m.timeline.pageDown()
	case "enter", "space", " ":
		m.timeline.toggleSelected()
	}
	return nil
}

func (m *UI) handleComposerKey(msg tea.KeyPressMsg) tea.Cmd {
	if isMultilineInsertKey(msg) {
		m.composer.insert("\n")
		m.resetInputHistoryBrowse()
		return nil
	}
	if cmd, ok := m.handleCommonShortcut(msg); ok {
		return cmd
	}
	switch msg.String() {
	case "esc":
		if m.session.Run.Running && m.live != nil {
			m.live.CancelTurn()
		}
	case "tab":
		m.timeline.enterBrowse()
	case "ctrl+l":
		m.session.SetCockpitExpanded(true)
		m.dashboardScroll = 0
	case "pgup":
		m.timeline.pageUp()
	case "pgdown":
		m.timeline.pageDown()
	case "up":
		m.previousInputHistory()
	case "down":
		m.nextInputHistory()
	case "enter":
		if text := m.composer.submit(); text != "" {
			m.rememberInput(text)
			return m.submit(text)
		}
	case "backspace":
		m.composer.backspace()
		m.resetInputHistoryBrowse()
	case "left":
		m.composer.left()
	case "right":
		m.composer.right()
	default:
		if msg.Text != "" {
			m.composer.insert(msg.Text)
			m.resetInputHistoryBrowse()
		}
	}
	return nil
}

func (m *UI) rememberInput(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.resetInputHistoryBrowse()
}

func (m *UI) previousInputHistory() {
	if len(m.inputHistory) == 0 {
		return
	}
	if !m.browsingInputHistory() {
		m.historyDraft = m.composer.text()
		m.historyPos = len(m.inputHistory)
	}
	if m.historyPos > 0 {
		m.historyPos--
	}
	m.composer.setText(m.inputHistory[m.historyPos])
}

func (m *UI) nextInputHistory() {
	if len(m.inputHistory) == 0 || !m.browsingInputHistory() {
		return
	}
	m.historyPos++
	if m.historyPos >= len(m.inputHistory) {
		m.composer.setText(m.historyDraft)
		m.resetInputHistoryBrowse()
		return
	}
	m.composer.setText(m.inputHistory[m.historyPos])
}

func (m *UI) resetInputHistoryBrowse() {
	m.historyPos = len(m.inputHistory)
	m.historyDraft = ""
}

func (m *UI) browsingInputHistory() bool {
	return m.historyPos >= 0 && m.historyPos < len(m.inputHistory)
}

func handleAddFormKey(msg tea.KeyPressMsg, eds []composer, idx *int, closeForm, submit func()) action {
	if len(eds) == 0 || idx == nil {
		return actionNone{}
	}
	switch msg.String() {
	case "esc":
		closeForm()
	case "tab", "down":
		*idx = (*idx + 1) % len(eds)
	case "shift+tab", "up":
		*idx = (*idx - 1 + len(eds)) % len(eds)
	case "enter":
		if *idx < len(eds)-1 {
			*idx++
			return actionNone{}
		}
		submit()
	case "ctrl+s":
		submit()
	case "backspace":
		eds[*idx].backspace()
	case "left":
		eds[*idx].left()
	case "right":
		eds[*idx].right()
	default:
		if msg.Text != "" {
			eds[*idx].insert(msg.Text)
		}
	}
	return actionNone{}
}

// handlePaste inserts clipboard/paste content into whatever currently owns
// input. An active overlay takes precedence (mirroring key routing): its top
// dialog consumes the paste if it has a text-entry sub-mode, else it's
// dropped rather than leaking into the cockpit behind. With no overlay, the
// intro pane or the main composer receives it.
func (m *UI) handlePaste(content string) {
	if m.overlay.active() {
		if p, ok := m.overlay.top().(paster); ok {
			p.handlePaste(content)
		}
		return
	}
	if m.intro != nil {
		m.intro.handlePaste(content)
		return
	}
	m.composer.insert(strings.ReplaceAll(content, "\r\n", "\n"))
}

// submit dispatches a submitted prompt: to the live-run hook when one is
// wired (its ConversationStarted event adds the user item), otherwise it
// echoes the prompt locally so the editor is usable without a runner.
// Slash commands (e.g. /clear) are handled here before dispatch.
// When a run is already active the input is queued for mid-turn injection;
// it renders in the transcript as a pending item and is picked up by the
// runner at the next Steerer drain point (or promoted to a follow-up turn
// on completion when the runner never reaches another drain).
func (m *UI) submit(text string) tea.Cmd {
	if strings.HasPrefix(text, "/") {
		return m.handleSlashSubmit(text)
	}
	if m.session.Run.Running && m.live != nil {
		m.live.QueueInput(text)
		m.timeline.addQueuedUser(text)
		return nil
	}
	if m.runFn != nil {
		return m.runFn(text)
	}
	m.timeline.addUser(text)
	return nil
}
