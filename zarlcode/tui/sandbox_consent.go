package tui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// SandboxConsent asks for explicit workspace-scoped consent to run shell tools
// without kernel confinement on macOS. Its zero value has not accepted; only an
// explicit y key accepts. Enter, n, Escape and Ctrl+C decline.
type SandboxConsent struct {
	accepted bool
	done     bool
}

// Init starts the consent screen without background work or a default answer.
func (m *SandboxConsent) Init() tea.Cmd { return nil }

// Update accepts only an explicit y key, ignores pasted input, and quits on a
// decision. Once a decision is made, later input cannot change it.
func (m *SandboxConsent) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done {
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "y", "Y":
			m.accepted, m.done = true, true
			return m, tea.Quit
		case "n", "N", "enter", "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View makes the absence of macOS kernel confinement and the scope of the saved
// choice explicit before any shell tool can run.
func (m *SandboxConsent) View() tea.View {
	return tea.NewView("\n  Shell sandbox unavailable on macOS\n\n" +
		"  The kernel shell sandbox currently requires Linux.\n" +
		"  Continuing lets shell tools run with your account's permissions,\n" +
		"  without kernel confinement. Shell policy checks still apply,\n" +
		"  but are not a replacement for a sandbox.\n\n" +
		"  y: continue unconfined and remember for this workspace\n" +
		"  n / Enter / Esc: exit without changing settings\n\n" +
		"  If you require confinement, use a supported Linux environment.\n")
}

// Accepted reports explicit acceptance, never an implicit or default choice.
func (m *SandboxConsent) Accepted() bool { return m.accepted }

func (p Launch) confirmUnconfinedShell(ctx context.Context) (bool, error) {
	if p.Headless {
		return false, errors.New("macOS shell sandbox requires Linux: explicitly set ZARLCODE_SANDBOX=0 to run unconfined, or use a supported Linux environment")
	}
	model := &SandboxConsent{}
	_, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if err != nil {
		return false, fmt.Errorf("shell sandbox consent: %w", err)
	}
	return model.Accepted(), nil
}
