package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// catalogEditedMsg reports that the external editor for an agent/skill file
// exited. The model reloads the catalog panes so the edit is reflected.
type catalogEditedMsg struct {
	path string
	err  error
}

// editFileCmd opens path in the configured editor, suspending the alt-screen
// for the duration (the same mechanism the OAuth flow uses). When the editor
// exits, a catalogEditedMsg fires so the panes reload.
func (m *UI) editFileCmd(path string) tea.Cmd {
	var configured string
	if m.settings != nil {
		configured = m.settings.Editor(m.appContext())
	}
	cmd := editorCommand(m.appContext(), path, configured)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return catalogEditedMsg{path: path, err: err}
	})
}

// editorCommand builds the editor invocation for path. The configured editor
// (the integrations setting) wins; empty falls back to ZARLCODE_EDITOR, then
// VISUAL, then EDITOR, then vi. The value may carry flags (e.g. "code -w" /
// "emacs -nw"), which are split off.
func editorCommand(ctx context.Context, path, configured string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	editor := strings.TrimSpace(configured)
	if editor == "" {
		editor = firstNonEmptyEnv("ZARLCODE_EDITOR", "VISUAL", "EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	fields := strings.Fields(editor)
	args := fields[1:]
	args = append(args, path)
	return exec.CommandContext(ctx, fields[0], args...)
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// handleCatalogEditMsg refreshes UI surfaces after an external edit and reports
// the outcome. Returns true when it consumed the message.
func (m *UI) handleCatalogEditMsg(msg tea.Msg) bool {
	ed, ok := msg.(catalogEditedMsg)
	if !ok {
		return false
	}
	if m.overlay.active() {
		if v, ok := m.overlay.top().(*fileViewer); ok {
			v.refreshEditedPath(ed.path)
		}
	}
	if d, ok := topSettingsDialog(m); ok {
		if d.agentsPane != nil {
			d.agentsPane.reload(d.s)
		}
		if d.skillsPane != nil {
			d.skillsPane.reload(d.s)
		}
		if d.hooksPane != nil {
			d.hooksPane.reload(d.s)
		}
	}
	switch {
	case ed.err != nil:
		m.session.SetErrorToast("editor: " + ed.err.Error())
	default:
		m.session.SetSuccessToast("saved " + shortenHome(ed.path))
	}
	return true
}
