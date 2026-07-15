package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *UI) toastExpiryCmd() tea.Cmd { return m.session.ToastExpiryCmd() }

// headerMode delegates to Session.headerMode, keeping plan/build/chat logic
// on the Session-owned run and mode state that panes also read.
func (m *UI) headerMode() string { return m.session.headerMode() }

func (m *UI) headerModeBadge() string {
	mode := m.headerMode()
	accent := palette.Assistant
	switch mode {
	case "build":
		accent = palette.Tool
	case "plan":
		accent = palette.PlanMode
	}
	return bracketed(accent.On(mode))
}

func (m *UI) statusHint() string {
	stopKey := "ctrl+c quit  ·  ctrl+q ctx"
	if m.session.Run.Running {
		stopKey = "esc stop  ·  ctrl+c quit  ·  ctrl+q ctx"
	}
	if m.session.CockpitExpanded {
		return " tab/←→ switch  ·  ↑↓/jk scroll  ·  pgup/pgdn page  ·  home/end jump  ·  ctrl+l / esc / q close  ·  " + stopKey + "  ·  ctrl+g keys"
	}
	if m.timeline.browsing {
		return " ↑↓/jk move  ·  enter expand  ·  pgup/pgdn page  ·  esc/i compose  ·  " + stopKey + "  ·  ctrl+g keys"
	}
	if m.session.PlanMode {
		return " enter submit  ·  shift+enter newline  ·  shift+tab build  ·  " + stopKey + "  ·  ctrl+g keys"
	}
	if hint := slashStatusHint(m.composer.text()); hint != "" {
		return hint
	}
	return m.attachmentSummary() + "enter submit  ·  shift+enter newline  ·  tab browse  ·  shift+tab plan mode  ·  " + stopKey + "  ·  ctrl+g keys"
}

// drawBar paints a single reverse-video bar across r, padded to the full
// width so the whole row reads as one bar.

// shortenHome replaces the user's home dir prefix in p with "~".
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// gitBranch returns the active branch from <root>/.git/HEAD, or "" when
// root isn't a repo or HEAD is detached.
func gitBranch(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	const prefix = "ref: refs/heads/"
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, prefix) {
		return ""
	}
	return s[len(prefix):]
}
