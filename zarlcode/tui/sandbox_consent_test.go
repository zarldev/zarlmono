package tui_test

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSandboxConsentRequiresExplicitKey(t *testing.T) {
	for _, tt := range []struct {
		name     string
		msg      tea.Msg
		accepted bool
		quit     bool
	}{
		{name: "yes", msg: tea.KeyPressMsg{Code: 'y'}, accepted: true, quit: true},
		{name: "no", msg: tea.KeyPressMsg{Code: 'n'}, quit: true},
		{name: "default no", msg: tea.KeyPressMsg{Code: tea.KeyEnter}, quit: true},
		{name: "escape", msg: tea.KeyPressMsg{Code: tea.KeyEscape}, quit: true},
		{name: "control c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, quit: true},
		{name: "pasted yes is not consent", msg: tea.PasteMsg{Content: "y"}},
		{name: "unrelated key", msg: tea.KeyPressMsg{Code: 'x'}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := &tui.SandboxConsent{}
			if model.Accepted() || model.Init() != nil {
				t.Fatal("startup consent has an implicit choice or side effect")
			}
			_, cmd := model.Update(tt.msg)
			if model.Accepted() != tt.accepted || (cmd != nil) != tt.quit {
				t.Fatalf("accepted=%v quit=%v", model.Accepted(), cmd != nil)
			}
			if cmd != nil {
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("decision did not exit startup program")
				}
				model.Update(tea.KeyPressMsg{Code: 'y'})
				if model.Accepted() != tt.accepted {
					t.Fatal("later input changed a completed decision")
				}
			}
		})
	}
}

func TestSandboxConsentExplainsRiskAndScope(t *testing.T) {
	view := (&tui.SandboxConsent{}).View().Content
	for _, text := range []string{"requires Linux", "without kernel confinement", "account's permissions", "remember for this workspace", "exit without changing settings"} {
		if !strings.Contains(view, text) {
			t.Errorf("consent omits %q", text)
		}
	}
}

func TestSandboxConsentThroughStartupProgram(t *testing.T) {
	for _, key := range []rune{'y', 'n', tea.KeyEscape} {
		t.Run(string(key), func(t *testing.T) {
			model := &tui.SandboxConsent{}
			var output bytes.Buffer
			program := tea.NewProgram(sandboxConsentEvent{SandboxConsent: model, key: key},
				tea.WithContext(t.Context()), tea.WithoutSignalHandler(),
				tea.WithInput(strings.NewReader("")), tea.WithOutput(&output),
				tea.WithWindowSize(80, 24))
			if _, err := program.Run(); err != nil {
				t.Fatal(err)
			}
			if model.Accepted() != (key == 'y') {
				t.Fatal("startup program lost consent decision")
			}
		})
	}
}

type sandboxConsentEvent struct {
	*tui.SandboxConsent
	key rune
}

func (m sandboxConsentEvent) Init() tea.Cmd {
	return func() tea.Msg { return tea.KeyPressMsg{Code: m.key} }
}
