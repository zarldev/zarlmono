package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *UI) toastExpiryCmd() tea.Cmd { return m.session.ToastExpiryCmd() }

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
