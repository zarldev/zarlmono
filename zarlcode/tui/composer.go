package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
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
	// Reserve the compact prompt glyph prefix ("› ") so a filled line is not
	// clipped after the styled prefix is prepended.
	wrapW := max(innerW-2, 1)

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
	border, accent, label := palette.BorderFocus, palette.Primary, ""
	if border == "" {
		border = palette.Primary
	}
	if planMode {
		border, accent = palette.PlanMode, palette.PlanMode
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
		m.overlay.push(newConversationActionsDialog())
		return nil
	}
	if msg.String() == "ctrl+n" {
		if m.intro != nil {
			return m.handleIntroKey(msg)
		}
		m.openSessionNameDialog()
		return nil
	}
	if cmd, ok := m.handleGlobalShortcut(msg); ok {
		return cmd
	}
	if m.intro != nil {
		return m.handleIntroKey(msg)
	}
	if m.startupFailure != nil {
		return m.handleStartupFailureKey(msg)
	}
	if m.session.CockpitExpanded {
		return m.handleDashboardKey(msg)
	}
	if m.timeline.browsing {
		return m.handleBrowseKey(msg)
	}
	return m.handleComposerKey(msg)
}

func (m *UI) handleIntroKey(msg tea.KeyPressMsg) tea.Cmd {
	// Startup owns tab/shift+tab for prompt/session focus. Handle those before
	// shell-wide shortcuts so shift+tab cannot accidentally toggle plan mode.
	if msg.String() == "tab" || msg.String() == "shift+tab" {
		return m.intro.handleKey(m, msg)
	}
	if cmd, ok := m.handleCommonShortcut(msg); ok {
		return cmd
	}
	return m.intro.handleKey(m, msg)
}

func (m *UI) handleStartupFailureKey(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.handleCommonShortcut(msg); ok {
		return cmd
	}
	return m.startupFailure.handleKey(msg)
}

func (m *UI) handleGlobalShortcut(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+w":
		m.overlay.push(newWorkingSetPane(m.appContext(), m.session, m.live, m.session.WorkspaceDir))
		return nil, true
	case "ctrl+y":
		if m.live != nil {
			m.overlay.push(newSteerTray(m.live))
		}
		return nil, true
	case "ctrl+o":
		m.overlay.push(newInspector(BuildInspectorSnapshot(m.appContext(), m.session, m.live, nil)))
		return nil, true
	case "ctrl+b":
		m.session.StateSidebarHidden = !m.session.StateSidebarHidden
		state := "shown"
		if m.session.StateSidebarHidden {
			state = "hidden"
		}
		m.session.SetToast("state bar " + state)
		return m.toastExpiryCmd(), true
	case "ctrl+r":
		m.overlay.push(newTranscriptReader(m.timeline))
		return nil, true
	case "ctrl+a":
		m.overlay.push(newAgentActivityScreen(m.timeline))
		return nil, true
	}
	return nil, false
}

func (m *UI) handleShellShortcut(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if cmd, ok := m.handleGlobalShortcut(msg); ok {
		return cmd, true
	}
	return m.handleCommonShortcut(msg)
}

func (m *UI) handleCommonShortcut(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+k":
		m.openCommandPalette()
		return nil, true
	case "ctrl+shift+c":
		return m.copyLastAssistantResponse(), true
	case "ctrl+f":
		viewer := newFileViewer(m.appContext(), m.session.WorkspaceDir)
		m.overlay.push(viewer)
		return m.fileViewerInitialCmd(viewer), true
	case "ctrl+e":
		return m.openModelQuickPick(), true
	case "ctrl+g":
		m.overlay.push(m.newHelpDialog())
		return nil, true
	case "ctrl+h":
		if m.settings != nil {
			m.overlay.push(newToolHistory(m.appContext(), m.settings.Store, m.session.ID))
		}
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

func (m *UI) handleDashboardKey(msg tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.handleShellShortcut(msg); ok {
		return cmd
	}
	switch msg.String() {
	case "tab", "right":
		m.contextView.nextTab()
	case "shift+tab", "left":
		m.contextView.prevTab()
	case "up", "k":
		m.contextView.scrollActiveBy(-1)
		m.clampContextViewScroll()
	case "down", "j":
		m.contextView.scrollActiveBy(1)
		m.clampContextViewScroll()
	case "pgup":
		m.contextView.scrollActiveBy(-m.dashboardPageStep())
		m.clampContextViewScroll()
	case "pgdown":
		m.contextView.scrollActiveBy(m.dashboardPageStep())
		m.clampContextViewScroll()
	case "home", "g":
		m.contextView.setActiveScroll(0)
	case "end":
		m.contextView.setActiveScroll(m.dashboardMaxScroll())
	case "esc", "ctrl+l", "q":
		m.session.SetCockpitExpanded(false)
		m.contextView = contextViewState{}
	}
	return nil
}

func (m *UI) handleBrowseKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.timeline.selectionActive() {
		return m.handleSelectionKey(msg)
	}
	// Browsing freezes the transcript viewport, not the composer. Dedicated
	// navigation keys and the documented v/i browse commands remain controls;
	// other printable text keeps editing the prompt while scrolled back.
	switch msg.String() {
	case "v":
		m.timeline.startSelection()
		return nil
	case "i":
		m.timeline.exitBrowse()
		return nil
	}
	if msg.Text != "" {
		return m.handleComposerKey(msg)
	}
	if m.composer.text() != "" {
		switch msg.String() {
		case "enter", "backspace", "left", "right":
			return m.handleComposerKey(msg)
		}
	}
	if cmd, ok := m.handleShellShortcut(msg); ok {
		return cmd
	}
	switch msg.String() {
	case "esc":
		m.timeline.exitBrowse()
	case "up":
		m.timeline.cursorUp()
	case "down":
		m.timeline.cursorDown()
	case "home":
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

func (m *UI) handleSelectionKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.timeline.cancelSelection()
	case "up", "k":
		m.timeline.moveSelection(-1)
	case "down", "j":
		m.timeline.moveSelection(1)
	case "pgup":
		m.timeline.moveSelection(-m.timeline.pageStep())
	case "pgdown":
		m.timeline.moveSelection(m.timeline.pageStep())
	case "g", "home":
		m.timeline.moveSelectionTop()
	case "G", "end":
		m.timeline.moveSelectionBottom()
	case "y":
		text := m.timeline.finishSelection()
		if text == "" {
			m.session.SetToast("nothing selected")
			return m.toastExpiryCmd()
		}
		lines := strings.Count(text, "\n") + 1
		m.session.SetSuccessToast(fmt.Sprintf("copied %d lines", lines))
		return tea.Batch(tea.SetClipboard(text), m.toastExpiryCmd())
	case "enter", "space", " ":
		m.session.SetToast("cancel selection before expanding")
		return m.toastExpiryCmd()
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
		m.contextView = contextViewState{}
	case "pgup":
		m.timeline.pageUp()
	case "pgdown":
		m.timeline.pageDown()
	case "up":
		m.previousInputHistory()
	case "down":
		m.nextInputHistory()
	case "enter":
		if text := strings.TrimSpace(m.composer.text()); text != "" {
			cmd, accepted := m.acceptSubmit(text)
			if accepted {
				m.composer.setText("")
				m.rememberInput(text)
				m.draftScheduleSuppressed = true
				return tea.Batch(cmd, m.clearDraftCmd())
			}
			return cmd
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
			if msg.Text == "@" && m.session.WorkspaceDir != "" {
				m.overlay.push(newFileMentionPicker(m.session.WorkspaceDir))
				return nil
			}
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
// The first real prompt also seeds a generated session label after slash-command
// handling and before dispatch; generated labels never claim manual provenance.
func (m *UI) submit(text string) tea.Cmd {
	cmd, _ := m.acceptSubmit(text)
	return cmd
}

func (m *UI) acceptSubmit(text string) (tea.Cmd, bool) {
	if strings.HasPrefix(text, "/") {
		return m.handleSlashSubmit(text), true
	}
	if m.session.Run.Running && m.live != nil {
		if len(m.pendingAttachments) > 0 {
			m.session.SetErrorToast("image attachments can only be sent with a new turn")
			return m.toastExpiryCmd(), false
		}
		m.live.QueueInput(text)
		m.timeline.addQueuedUser(text)
		return nil, true
	}
	m.generateFirstPromptLabel(text)
	if m.live != nil {
		attachments := m.attachmentParts()
		attachmentMetadata := attachmentMetadataOf(m.pendingAttachments)
		m.pendingAttachments = nil
		if !m.startupReady {
			m.startupPrompt = text
			m.startupAttachments = attachments
			m.startupAttachmentMetadata = attachmentMetadata
			m.session.SetToast("finishing startup before the first turn…")
			return m.toastExpiryCmd(), true
		}
		m.session.SetSubmittedAttachments(attachmentMetadata)
		return RunFnWithAttachments(engine.WithToolOutputSession(m.appContext(), m.session.ID), m.live, text, attachments), true
	}
	if m.runFn != nil {
		return m.runFn(text), true
	}
	m.timeline.addUser(text)
	return nil, true
}

func (m *UI) generateFirstPromptLabel(prompt string) {
	if m.session.LabelManual || m.session.Label != "" {
		return
	}
	m.session.Label = normalizeSessionLabel(prompt)
}
