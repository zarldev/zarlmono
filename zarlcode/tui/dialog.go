package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// Dialogs are intent-returning: a dialog handles a key and returns a
// typed action (an intent), NOT a tea.Cmd. The root model translates the
// action into real effects in handleAction. This keeps dialogs pure and
// the side-effect ownership in one place.
type action interface{ isAction() }

type (
	actionNone         struct{} // key consumed, nothing to do
	actionClose        struct{} // pop the topmost dialog
	actionQuit         struct{} // quit the program
	actionClearContext struct{} // clear conversation context and transcript
	actionCompactNow   struct{} // compact conversation context now
)

func (actionNone) isAction()         {}
func (actionClose) isAction()        {}
func (actionQuit) isAction()         {}
func (actionClearContext) isAction() {}
func (actionCompactNow) isAction()   {}

// actionRunCommand dispatches one command-palette selection.
type actionRunCommand struct{ id CommandID }

func (actionRunCommand) isAction() {}

// actionSetTheme switches the active colour theme by name.
type actionSetTheme struct{ name string }

func (actionSetTheme) isAction() {}

// actionSetSessionLabel applies a user-authored session label.
type actionSetSessionLabel struct{ label string }

func (actionSetSessionLabel) isAction() {}

// actionDeleteSession permanently deletes one saved session after confirmation.
type actionDeleteSession struct{ id string }

func (actionDeleteSession) isAction() {}

// actionPush opens a nested dialog over the current one (e.g. the providers
// manager from the settings overlay, or the add-provider form from there).
type actionPush struct{ d dialog }

func (actionPush) isAction() {}

// actionOAuthLogin starts an OAuth sign-in flow for provider. The root
// turns it into the browser-open + callback-await tea.Cmd (dialogs can't
// run commands themselves).
type actionOAuthLogin struct{ provider string }

func (actionOAuthLogin) isAction() {}

// actionFetchModels requests an async model-list fetch for provider; the
// root turns it into a tea.Cmd that probes the provider and returns a
// modelsLoadedMsg.
type actionFetchModels struct{ provider string }

func (actionFetchModels) isAction() {}

// actionFileViewerEntries requests an asynchronous directory listing. resolvePath
// is used by newFileViewerAt: the command resolves whether path is a file or
// directory and returns the appropriate directory + selected file name.
type actionFileViewerEntries struct {
	viewer      *fileViewer
	requestID   uint64
	dir         string
	path        string
	resolvePath bool
	selectName  string
	ctx         context.Context
}

func (actionFileViewerEntries) isAction() {}

// actionFileViewerPreview requests asynchronous file/directory preview work.
type actionFileViewerPreview struct {
	viewer    *fileViewer
	requestID uint64
	path      string
	directory bool
	ctx       context.Context
}

func (actionFileViewerPreview) isAction() {}

// actionEditFile opens path in the user's $EDITOR. The root turns it into a
// tea.ExecProcess command (suspending the alt-screen) and reloads the catalog
// panes when the editor exits. Used by the agents / skills managers.
type actionEditFile struct{ path string }

func (actionEditFile) isAction() {}

// actionAttachImage attaches an image path to the next submitted prompt.
type actionAttachImage struct{ path string }

func (actionAttachImage) isAction() {}

type actionAttachFile struct{ path string }

func (actionAttachFile) isAction() {}

type actionCopyText struct{ text string }

func (actionCopyText) isAction() {}

// actionRollback restores files from a recorded checkpoint after a confirmation
// dialog. Empty path means rollback the whole turn.
type actionRollback struct {
	turnID string
	path   string
}

func (actionRollback) isAction() {}

// actionKillProcess requests termination of a background bash process from the
// inspector. The root runs the side effect asynchronously and feeds the result
// back into the live agent context.
type actionKillProcess struct {
	processID string
	signal    string
}

func (actionKillProcess) isAction() {}

// dialog is a modal overlay. handleKey returns an action; draw paints the
// dialog (centered) over area.
type dialog interface {
	handleKey(tea.KeyPressMsg) action
	draw(scr uv.Screen, area uv.Rectangle)
}

// fullScreener is implemented by dialogs that paint the entire screen (the
// settings surface). The root then skips the panes + global status bar behind
// them, so the takeover owns the whole frame and its single footer.
type fullScreener interface{ fullScreen() bool }

// paster is implemented by dialogs with a text-entry sub-mode that should
// accept clipboard content. Paste arrives as its own tea.PasteMsg (never a
// KeyPressMsg), so the root routes it to the top dialog when it satisfies
// this; non-pasters drop it rather than leaking it to the cockpit behind.
type paster interface{ handlePaste(string) }

// overlay is a stack of dialogs. The topmost receives input; all are
// drawn bottom-to-top so stacked modals layer correctly.
type overlay struct{ stack []dialog }

func (o *overlay) active() bool  { return len(o.stack) > 0 }
func (o *overlay) top() dialog   { return o.stack[len(o.stack)-1] }
func (o *overlay) push(d dialog) { o.stack = append(o.stack, d) }

// coversScreen reports whether any dialog in the stack is a full-screen
// takeover — so a picker pushed over the settings surface still hides the
// panes behind (the base, not the top, decides coverage).
func (o *overlay) coversScreen() bool {
	for _, d := range o.stack {
		if fs, ok := d.(fullScreener); ok && fs.fullScreen() {
			return true
		}
	}
	return false
}

func (o *overlay) pop() {
	if len(o.stack) > 0 {
		if closer, ok := o.stack[len(o.stack)-1].(interface{ close() }); ok {
			closer.close()
		}
		o.stack = o.stack[:len(o.stack)-1]
	}
}

func (o *overlay) draw(scr uv.Screen, area uv.Rectangle) {
	for _, d := range o.stack {
		d.draw(scr, area)
	}
}

// dismissConversationDialogs closes the active conversation action and any
// confirmation layered over it without disturbing an unrelated parent overlay.
func (m *UI) dismissConversationDialogs() {
	m.overlay.pop()
	for m.overlay.active() {
		if _, ok := m.overlay.top().(*conversationActionsDialog); !ok {
			return
		}
		m.overlay.pop()
	}
}

// handleAction translates a dialog's intent into a model effect.
func (m *UI) handleAction(a action) tea.Cmd {
	switch a := a.(type) {
	case actionClose:
		m.overlay.pop()
		if !m.overlay.active() {
			// Overlay fully dismissed — if the settings changed the active
			// provider, re-point the live runner so it takes effect now.
			return m.maybeRepoint()
		}
		// A nested picker closed back onto the settings surface: drain any
		// queued model fetch (e.g. after the compaction provider changed).
		if d, ok := topSettingsDialog(m); ok {
			if p := d.takePendingFetch(); p != "" {
				return m.fetchModelsCmd(p)
			}
		}
	case actionQuit:
		m.cancelLiveTurnForQuit()
		return tea.Quit
	case actionClearContext:
		m.dismissConversationDialogs()
		return m.clearContextAndTimeline()
	case actionCompactNow:
		m.dismissConversationDialogs()
		return m.compactNowCmd()
	case actionSetTheme:
		if t, ok := theme.ByName(a.name); ok {
			UseTheme(t)
		}
		m.overlay.pop()
	case actionPush:
		if a.d != nil {
			m.overlay.push(a.d)
			if viewer, ok := a.d.(*fileViewer); ok {
				return m.fileViewerInitialCmd(viewer)
			}
		}
	case actionSetSessionLabel:
		m.overlay.pop()
		return m.setSessionLabel(a.label)
	case actionRunCommand:
		m.overlay.pop()
		entry, ok := commandEntry(a.id)
		if !ok {
			return nil
		}
		return entry.run(m)
	case actionDeleteSession:
		m.overlay.pop()
		return m.deleteIntroSession(a.id)
	case actionFileViewerEntries:
		return fileViewerEntriesCmd(a)
	case actionFileViewerPreview:
		return fileViewerPreviewCmd(a)
	case actionOAuthLogin:
		return m.startOAuthLogin(a.provider)
	case actionFetchModels:
		return m.fetchModelsCmd(a.provider)
	case actionEditFile:
		return m.editFileCmd(a.path)
	case actionAttachImage:
		if err := m.attachImagePath(a.path); err != nil {
			m.session.SetErrorToast(err.Error())
		} else {
			m.session.SetSuccessToast("attached " + filepath.Base(a.path) + " to next prompt")
		}
	case actionAttachFile:
		m.overlay.pop()
		if err := m.attachFilePath(a.path); err != nil {
			m.session.SetErrorToast(err.Error())
		} else {
			rel, _ := filepath.Rel(m.session.WorkspaceDir, a.path)
			m.composer.insert("@" + filepath.ToSlash(rel) + " ")
			m.session.SetSuccessToast("attached " + rel + " to next prompt")
		}
		return m.toastExpiryCmd()
	case actionCopyText:
		if a.text != "" {
			m.session.SetSuccessToast("copied transcript selection")
			return tea.Batch(tea.SetClipboard(a.text), m.toastExpiryCmd())
		}
		return m.toastExpiryCmd()
	case actionRollback:
		return m.rollback(a.turnID, a.path)
	case actionResumeSession:
		m.overlay.pop()
		return m.completeResumeSession(a.session, a.useSaved)
	case serviceAction:
		return m.handleServiceAction(a)
	case actionKillProcess:
		return m.killProcessCmd(a.processID, a.signal)
	case actionAskpassReply:
		m.overlay.pop()
		if a.Reply != nil {
			resp := askpass.Response{Password: a.Password}
			if a.Cancel {
				resp = askpass.Response{Error: "cancelled"}
			}
			select {
			case a.Reply <- resp:
			default:
			}
		}
	}
	return nil
}

// --- help dialog ---

type helpDialog struct {
	sections []helpSection
	scroll   int
	viewport int
}

func (m *UI) newHelpDialog() *helpDialog {
	switch {
	case m.intro != nil:
		return &helpDialog{sections: startupHelpSections()}
	case m.session.CockpitExpanded:
		return &helpDialog{sections: dashboardHelpSections()}
	case m.timeline.browsing:
		return &helpDialog{sections: browseHelpSections()}
	default:
		return &helpDialog{sections: composeHelpSections()}
	}
}

func (d *helpDialog) handleKey(msg tea.KeyPressMsg) action {
	lines := d.lines()
	maxScroll := max(0, len(lines)-max(1, d.viewport))
	switch msg.String() {
	case "esc", "enter", "ctrl+g", "q":
		return actionClose{}
	case "up", "k":
		d.scroll = max(0, d.scroll-1)
	case "down", "j":
		d.scroll = min(maxScroll, d.scroll+1)
	case "pgup":
		d.scroll = max(0, d.scroll-max(1, d.viewport-1))
	case "pgdown":
		d.scroll = min(maxScroll, d.scroll+max(1, d.viewport-1))
	case "home", "g":
		d.scroll = 0
	case "end", "G":
		d.scroll = maxScroll
	}
	return actionNone{}
}

func (d *helpDialog) draw(scr uv.Screen, area uv.Rectangle) {
	lines := d.lines()
	width := min(104, max(40, area.Dx()))
	height := min(max(8, len(lines)+4), area.Dy())
	lay, ok := drawDialogPane(scr, area, "keys", width, height, palette.Border, palette.Primary)
	if !ok {
		return
	}
	d.viewport = lay.Body.Dy()
	maxScroll := max(0, len(lines)-d.viewport)
	d.scroll = min(max(d.scroll, 0), maxScroll)
	context := "contextual shortcuts"
	if maxScroll > 0 {
		context += " · " + helpScrollPosition(d.scroll, d.viewport, len(lines))
	}
	drawLine(scr, lay.Context, palette.Subtle.On(context))
	for i, line := range lines[d.scroll:min(len(lines), d.scroll+d.viewport)] {
		drawLine(scr, uv.Rect(lay.Body.Min.X, lay.Body.Min.Y+i, lay.Body.Dx(), 1), ansi.Truncate(line, lay.Body.Dx(), "…"))
	}
	footer := keyLegend(keyHint{"↑↓/jk", "scroll"}, keyHint{"pgup/pgdn", "page"}, keyHint{"ctrl+g / esc", "close"})
	drawLine(scr, lay.Footer, palette.Muted.On(footer))
}

func (d *helpDialog) lines() []string {
	sections := d.sections
	if len(sections) == 0 {
		sections = composeHelpSections()
	}
	return helpLines(sections)
}

func helpScrollPosition(scroll, viewport, total int) string {
	return fmt.Sprintf("%d–%d of %d", min(scroll+1, total), min(scroll+viewport, total), total)
}

func composeHelpSections() []helpSection {
	return []helpSection{
		{
			title: "compose",
			rows: [][]keyHint{
				{{"enter", "submit prompt"}, {"shift+enter", "newline"}, {"@", "attach file"}},
				{{"tab", "browse transcript"}, {"ctrl+r", "transcript reader"}, {"ctrl+k", "command palette"}},
				{{"shift+tab", "plan ⇄ build"}, {"ctrl+l", "context dashboard"}, {"esc", "stop current turn"}},
			},
		},
		{
			title: "quick panes",
			rows: [][]keyHint{
				{{"ctrl+f", "file viewer"}, {"ctrl+e", "model picker"}, {"ctrl+s", "settings"}},
				{{"ctrl+p", "plan pane"}, {"ctrl+w", "working set"}, {"ctrl+o", "inspector"}},
				{{"ctrl+a", "agent activity"}, {"ctrl+h", "tool history"}, {"ctrl+t", "theme"}},
				{{"ctrl+y", "execution tray"}, {"ctrl+b", "toggle state bar"}, {"ctrl+n", "name session"}},
			},
		},
		{
			title: "slash commands",
			rows:  slashCommandRows(),
		},
		{title: "global", rows: [][]keyHint{{{"ctrl+g", "close this help"}, {"ctrl+q", "conversation"}, {"ctrl+c", "quit"}}}}}
}

func startupHelpSections() []helpSection {
	return []helpSection{
		{
			title: "startup",
			rows: [][]keyHint{
				{{"tab", "prompt ⇄ sessions"}, {"enter", "start / resume"}, {"shift+enter / ctrl+j", "newline"}},
				{{"↑↓ / j k", "select session"}, {"pgup / pgdn", "page sessions"}, {"home/end", "top / bottom"}},
			},
		},
		{title: "global", rows: [][]keyHint{{{"ctrl+g", "close this help"}, {"ctrl+c", "quit"}}}},
	}
}

func browseHelpSections() []helpSection {
	return []helpSection{
		{
			title: "browse transcript",
			rows: [][]keyHint{
				{{"↑↓ / j k", "move"}, {"pgup / pgdn", "page"}, {"g/home / G/end", "top / bottom"}},
				{{"v", "select lines"}, {"y", "copy selection"}, {"esc", "cancel selection / compose"}},
				{{"enter / space", "expand / collapse"}, {"i / esc", "back to compose"}},
			},
		},
		{title: "quick panes", rows: [][]keyHint{{{"ctrl+f", "file viewer"}, {"ctrl+e", "model picker"}, {"ctrl+b", "toggle state bar"}}}},
		{title: "global", rows: [][]keyHint{{{"ctrl+g", "close this help"}, {"ctrl+c", "quit"}}}},
	}
}

func dashboardHelpSections() []helpSection {
	return []helpSection{
		{
			title: "context dashboard",
			rows: [][]keyHint{
				{{"tab / shift+tab / ←→", "switch tabs"}, {"↑↓ / j k", "scroll"}},
				{{"pgup / pgdn", "page"}, {"home/end", "top / bottom"}, {"esc / q / ctrl+l", "compose"}},
			},
		},
		{title: "quick panes", rows: [][]keyHint{{{"ctrl+f", "file viewer"}, {"ctrl+e", "model picker"}}}},
		{title: "global", rows: [][]keyHint{{{"ctrl+g", "close this help"}, {"ctrl+c", "quit"}}}},
	}
}

type helpSection struct {
	title string
	rows  [][]keyHint
}

func slashCommandRows() [][]keyHint {
	rows := make([][]keyHint, 0, len(slashCommands))
	for _, c := range slashCommands {
		rows = append(rows, []keyHint{{key: c.name, label: c.desc}})
	}
	return rows
}

func helpLines(sections []helpSection) []string {
	var lines []string
	for _, s := range sections {
		if len(s.rows) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, palette.Subtle.On(s.title))
		for _, row := range s.rows {
			if len(row) == 0 {
				continue
			}
			lines = append(lines, "  "+keyLegend(row...))
		}
	}
	return lines
}

func drawActionDialog(scr uv.Screen, area uv.Rectangle, title, context string, body []string, footer string, width int) {
	height := len(body) + 4
	lay, ok := drawDialogPane(scr, area, title, width, height, palette.Border, palette.Primary)
	if !ok {
		return
	}
	drawLine(scr, lay.Context, palette.Subtle.On(context))
	for i, line := range body[:min(len(body), lay.Body.Dy())] {
		drawLine(scr, uv.Rect(lay.Body.Min.X, lay.Body.Min.Y+i, lay.Body.Dx(), 1), line)
	}
	drawLine(scr, lay.Footer, palette.Muted.On(footer))
}

// centerRect computes a centered rectangle and clamps it to area. It is shared
// by modal renderers that paint into an existing uv.Screen instead of returning
// a pre-rendered lipgloss block.
func centerRect(area uv.Rectangle, w, h int) uv.Rectangle {
	if w > area.Dx() {
		w = area.Dx()
	}
	if h > area.Dy() {
		h = area.Dy()
	}
	return uv.Rect(
		area.Min.X+(area.Dx()-w)/2,
		area.Min.Y+(area.Dy()-h)/2,
		w, h,
	)
}

// --- quit confirmation dialog ---

// quitConfirmDialog is a small modal that asks "Quit zarlcode?" before
// exiting. y/enter confirms; anything else dismisses.
type quitConfirmDialog struct{}

func newQuitConfirmDialog() *quitConfirmDialog { return &quitConfirmDialog{} }

func (quitConfirmDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "y", "Y", "enter":
		return actionQuit{}
	}
	return actionClose{}
}

func (quitConfirmDialog) draw(scr uv.Screen, area uv.Rectangle) {
	drawActionDialog(scr, area, "quit", "confirm", []string{
		palette.Warning.On("quit " + appDisplayName + "?"),
	}, keyLegend(keyHint{"y / enter", "confirm"}, keyHint{"any other key", "cancel"}), 64)
}

// --- conversation actions dialog ---

type conversationActionsDialog struct{}

func newConversationActionsDialog() *conversationActionsDialog { return &conversationActionsDialog{} }

func (conversationActionsDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "c", "C", "enter":
		return actionCompactNow{}
	case "x", "X", "delete", "backspace":
		return actionPush{d: newClearContextConfirmDialog()}
	}
	return actionClose{}
}

func (conversationActionsDialog) draw(scr uv.Screen, area uv.Rectangle) {
	drawActionDialog(scr, area, "conversation", "context", []string{
		palette.Primary.On("conversation context"),
		palette.Muted.On("Compact keeps the transcript visible but shrinks what the next turn remembers."),
		palette.Muted.On("Clear drops the transcript and live conversation context."),
	}, keyLegend(keyHint{"c / enter", "compact now"}, keyHint{"x", "clear…"}, keyHint{"any other", "cancel"}), 80)
}

// clearContextConfirmDialog asks before dropping the live conversation context
// and visible transcript. y/enter confirms; anything else dismisses.
type clearContextConfirmDialog struct{}

func newClearContextConfirmDialog() *clearContextConfirmDialog { return &clearContextConfirmDialog{} }

func (clearContextConfirmDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "y", "Y", "enter":
		return actionClearContext{}
	}
	return actionClose{}
}

func (clearContextConfirmDialog) draw(scr uv.Screen, area uv.Rectangle) {
	drawActionDialog(scr, area, "clear", "reset conversation", []string{
		palette.Primary.On("clear conversation context?"),
		palette.Muted.On("This clears the transcript and what the next turn remembers."),
		palette.Muted.On("It does not revert files or stop background processes."),
	}, keyLegend(keyHint{"y / enter", "confirm"}, keyHint{"any other key", "cancel"}), 76)
}

// deleteSessionConfirmDialog asks before permanently deleting a saved session.
type deleteSessionConfirmDialog struct {
	id    string
	label string
}

func newDeleteSessionConfirmDialog(id, label string) *deleteSessionConfirmDialog {
	return &deleteSessionConfirmDialog{id: id, label: label}
}

func (d deleteSessionConfirmDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "y", "Y", "enter":
		return actionDeleteSession{id: d.id}
	default:
		return actionClose{}
	}
}

func (d deleteSessionConfirmDialog) draw(scr uv.Screen, area uv.Rectangle) {
	label := strings.TrimSpace(d.label)
	if label == "" {
		label = "Unnamed session"
	}
	drawActionDialog(scr, area, "delete session", "confirm", []string{
		palette.Warning.On("permanently delete this session?"),
		palette.Primary.On(truncateRunes(label, 64)),
		palette.Muted.On("The saved conversation cannot be recovered."),
	}, keyLegend(keyHint{"y / enter", "delete"}, keyHint{"any other key", "cancel"}), 76)
}
