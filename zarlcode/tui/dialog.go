package tui

import (
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
)

func (actionNone) isAction()         {}
func (actionClose) isAction()        {}
func (actionQuit) isAction()         {}
func (actionClearContext) isAction() {}

// actionSetTheme switches the active colour theme by name.
type actionSetTheme struct{ name string }

func (actionSetTheme) isAction() {}

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

// actionEditFile opens path in the user's $EDITOR. The root turns it into a
// tea.ExecProcess command (suspending the alt-screen) and reloads the catalog
// panes when the editor exits. Used by the agents / skills managers.
type actionEditFile struct{ path string }

func (actionEditFile) isAction() {}

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
		o.stack = o.stack[:len(o.stack)-1]
	}
}

func (o *overlay) draw(scr uv.Screen, area uv.Rectangle) {
	for _, d := range o.stack {
		d.draw(scr, area)
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
		m.overlay.pop()
		return m.clearContextAndTimeline()
	case actionSetTheme:
		if t, ok := theme.ByName(a.name); ok {
			UseTheme(t)
		}
		m.overlay.pop()
	case actionPush:
		if a.d != nil {
			m.overlay.push(a.d)
		}
	case actionOAuthLogin:
		return m.startOAuthLogin(a.provider)
	case actionFetchModels:
		return m.fetchModelsCmd(a.provider)
	case actionEditFile:
		return m.editFileCmd(a.path)
	case actionRollback:
		return m.rollback(a.turnID, a.path)
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
}

func newHelpDialog() *helpDialog { return &helpDialog{sections: composeHelpSections()} }

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

func (helpDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "enter", "ctrl+g", "q":
		return actionClose{}
	}
	return actionNone{}
}

func (d helpDialog) draw(scr uv.Screen, area uv.Rectangle) {
	sections := d.sections
	if len(sections) == 0 {
		sections = composeHelpSections()
	}
	lines := helpLines(sections)
	drawDialogBox(scr, area, "keys", lines)
}

func composeHelpSections() []helpSection {
	return []helpSection{
		{
			title: "compose",
			rows: [][]keyHint{
				{{"enter", "submit prompt"}, {"shift+enter", "newline"}, {"tab", "browse transcript"}},
				{{"shift+tab", "plan ⇄ build"}, {"ctrl+l", "context dashboard"}, {"esc", "stop current turn"}},
			},
		},
		{
			title: "quick panes",
			rows: [][]keyHint{
				{{"ctrl+f", "file viewer"}, {"ctrl+e", "model picker"}, {"ctrl+s", "settings"}},
				{{"ctrl+p", "plan pane"}, {"ctrl+w", "working set"}, {"ctrl+o", "inspector"}},
			},
		},
		{
			title: "slash commands",
			rows: [][]keyHint{
				slashCommandHints(),
			},
		},
		{title: "global", rows: [][]keyHint{{{"ctrl+g", "close this help"}, {"ctrl+q", "clear context"}, {"ctrl+c", "quit"}}}}}
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
				{{"↑↓ / j k", "move"}, {"pgup / pgdn", "page"}, {"g/home / end", "top / bottom"}},
				{{"enter / space", "expand / collapse"}, {"i / esc", "back to compose"}},
			},
		},
		{title: "quick panes", rows: [][]keyHint{{{"ctrl+f", "file viewer"}, {"ctrl+e", "model picker"}}}},
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

func slashCommandHints() []keyHint {
	hints := make([]keyHint, 0, len(slashCommands))
	for _, c := range slashCommands {
		hints = append(hints, keyHint{key: c.name, label: c.desc})
	}
	return hints
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

// --- centered box drawing ---

// drawDialogBox paints a centered, bordered dialog over area without blanking
// the background. Only the dialog frame and content are drawn; the area around
// it is left untouched so the underlying UI remains visible (critical for theme
// preview where the user needs to see colours on real content).
func drawDialogBox(scr uv.Screen, area uv.Rectangle, title string, lines []string) {
	drawDialogBoxColored(scr, area, title, lines, 0, palette.Border, palette.Primary)
}

// drawDialogBoxColored is drawDialogBox with an explicit minimum body width and
// border/label colours. It is the single auto-sizing centered-box renderer,
// shared by the plain notice/confirm dialogs and the live plan pane (which
// wants a PlanMode tint and a minimum width).
func drawDialogBoxColored(scr uv.Screen, area uv.Rectangle, title string, lines []string, minW int, border, labelCol theme.Color) {
	w := max(minW, ansi.StringWidth(title))
	for _, l := range lines {
		if n := ansi.StringWidth(l); n > w {
			w = n
		}
	}
	w += 4 // borders + one-column padding on both sides.
	h := len(lines) + 2
	r := centerRect(area, w, h)
	inner := drawFrame(scr, r, frameStyle{Label: title, Border: border, LabelColor: labelCol})
	innerW := inner.Dx() - 2
	if innerW < 1 {
		return
	}
	for i, l := range lines {
		if i >= inner.Dy() {
			break
		}
		drawPaddedLine(scr, uv.Rect(inner.Min.X+1, inner.Min.Y+i, innerW, 1), l)
	}
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
	lines := []string{
		overlayTopBar("quit", nil, 0, "confirm", 72),
		palette.Subtle.On(strings.Repeat("─", 72)),
		palette.Warning.On("quit " + appDisplayName + "?"),
		"",
		palette.Subtle.On("y / enter") + palette.Muted.On("  confirm"),
		palette.Subtle.On("any other key") + palette.Muted.On("  cancel"),
	}
	drawDialogBox(scr, area, "quit", lines)
}

// --- clear context confirmation dialog ---

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
	lines := []string{
		overlayTopBar("clear", nil, 0, "reset", 72),
		palette.Subtle.On(strings.Repeat("─", 72)),
		palette.Primary.On("clear conversation context?"),
		palette.Muted.On("This clears the transcript and what the next turn remembers."),
		palette.Muted.On("It does not revert files or stop background processes."),
		"",
		palette.Subtle.On("y / enter") + palette.Muted.On("  confirm"),
		palette.Subtle.On("any other key") + palette.Muted.On("  cancel"),
	}
	drawDialogBox(scr, area, "clear", lines)
}
