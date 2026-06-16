package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

var errVaultUnlockCancelled = errors.New("vault unlock cancelled")

type vaultUnlockField int

const (
	vaultUnlockFieldPass vaultUnlockField = iota
	vaultUnlockFieldConfirm
)

type vaultUnlockModel struct {
	setup bool
	retry bool

	width  int
	height int

	field   vaultUnlockField
	pass    []rune
	confirm []rune
	err     string

	done bool
	out  string
}

func newVaultUnlockModel(setup, retry bool) *vaultUnlockModel {
	m := &vaultUnlockModel{setup: setup, retry: retry}
	if retry {
		m.err = "passphrase incorrect — try again"
	}
	return m
}

func (m *vaultUnlockModel) Init() tea.Cmd { return nil }

func (m *vaultUnlockModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case tea.PasteMsg:
		m.insert(msg.Content)
	case tea.ClipboardMsg:
		m.insert(msg.Content)
	}
	return m, nil
}

func (m *vaultUnlockModel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "esc":
		return tea.Quit
	case "enter":
		return m.submit()
	case "tab", "shift+tab":
		if m.setup {
			m.toggleField()
		}
	case "backspace":
		m.backspace()
	case "delete":
		m.clearActive()
	default:
		if msg.Text != "" {
			m.insert(msg.Text)
		}
	}
	return nil
}

func (m *vaultUnlockModel) submit() tea.Cmd {
	m.err = ""
	if !m.setup {
		if len(m.pass) == 0 {
			m.err = "passphrase required"
			return nil
		}
		m.done = true
		m.out = string(m.pass)
		return tea.Quit
	}

	if m.field == vaultUnlockFieldPass {
		if len(m.pass) == 0 {
			m.err = "empty passphrase"
			return nil
		}
		m.field = vaultUnlockFieldConfirm
		return nil
	}
	if len(m.pass) == 0 {
		m.err = "empty passphrase"
		m.field = vaultUnlockFieldPass
		return nil
	}
	if string(m.pass) != string(m.confirm) {
		m.err = "passphrases did not match"
		m.confirm = nil
		m.field = vaultUnlockFieldConfirm
		return nil
	}
	m.done = true
	m.out = string(m.pass)
	return tea.Quit
}

func (m *vaultUnlockModel) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.WindowTitle = appDisplayName + " vault"
	if m.width <= 0 || m.height <= 0 {
		return v
	}
	canvas := uv.NewScreenBuffer(m.width, m.height)
	drawSplash(canvas, canvas.Bounds(), palette.Primary, m.infoLines())
	v.Content = strings.ReplaceAll(canvas.Render(), "\r\n", "\n")
	return v
}

func (m *vaultUnlockModel) infoLines() []string {
	lines := []string{
		palette.Subtle.On("vault unlock"),
		palette.Muted.On(m.subtitle()),
	}
	if m.err != "" {
		lines = append(lines, palette.Error.On(m.err))
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, m.promptLines()...)
	lines = append(lines, "")
	lines = append(lines, m.footer())
	return lines
}

func (m *vaultUnlockModel) subtitle() string {
	if m.setup {
		return "set a passphrase to encrypt zarlcode credentials at rest"
	}
	return "enter the passphrase for stored API keys and OAuth tokens"
}

func (m *vaultUnlockModel) promptLines() []string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	border := palette.Primary
	accent := palette.Primary
	return splashPromptBoxLines(w, border, accent, func(textWidth int) []string {
		return m.promptDisplayLines(textWidth)
	})
}

func (m *vaultUnlockModel) promptDisplayLines(width int) []string {
	if width < 1 {
		width = 1
	}
	if m.setup {
		pass := m.fieldLine("new passphrase", m.masked(m.pass, m.field == vaultUnlockFieldPass), width)
		confirm := m.fieldLine("confirm", m.masked(m.confirm, m.field == vaultUnlockFieldConfirm), width)
		return []string{pass, confirm}
	}
	return []string{m.masked(m.pass, true)}
}

func (m *vaultUnlockModel) fieldLine(label, value string, width int) string {
	labelText := fmt.Sprintf("%-14s", label)
	line := palette.Muted.On(labelText) + value
	return ansi.Truncate(line, width, "")
}

func (m *vaultUnlockModel) masked(rs []rune, focused bool) string {
	if len(rs) == 0 {
		placeholder := palette.Muted.On("passphrase")
		if focused {
			placeholder += palette.Primary.On("▏")
		}
		return placeholder
	}
	masked := strings.Repeat("•", len(rs))
	if focused {
		masked += palette.Primary.On("▏")
	}
	return masked
}

func (m *vaultUnlockModel) footer() string {
	key := func(k string) string { return palette.Subtle.On(k) }
	mut := func(s string) string { return palette.Muted.On(s) }
	parts := []string{key("enter") + mut(" submit")}
	if m.setup {
		parts = append(parts, key("tab")+mut(" switch field"))
	}
	parts = append(parts, key("esc")+mut(" skip vault"), key("ctrl+c")+mut(" skip vault"))
	return strings.Join(parts, mut("    "))
}

func (m *vaultUnlockModel) toggleField() {
	if m.field == vaultUnlockFieldPass {
		m.field = vaultUnlockFieldConfirm
		return
	}
	m.field = vaultUnlockFieldPass
}

func (m *vaultUnlockModel) active() *[]rune {
	if m.setup && m.field == vaultUnlockFieldConfirm {
		return &m.confirm
	}
	return &m.pass
}

func (m *vaultUnlockModel) insert(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "")
	if s == "" {
		return
	}
	active := m.active()
	*active = append(*active, []rune(s)...)
	m.err = ""
}

func (m *vaultUnlockModel) backspace() {
	active := m.active()
	if len(*active) == 0 {
		return
	}
	*active = (*active)[:len(*active)-1]
	m.err = ""
}

func (m *vaultUnlockModel) clearActive() {
	active := m.active()
	*active = nil
	m.err = ""
}

func runVaultUnlockSplash(ctx context.Context, setup, retry bool) (string, error) {
	return runVaultUnlockSplashWithProgram(ctx, setup, retry, nil)
}

func runVaultUnlockSplashWithProgram(ctx context.Context, setup, retry bool, opts []tea.ProgramOption) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	model := newVaultUnlockModel(setup, retry)
	programOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithoutSignalHandler()}
	programOpts = append(programOpts, opts...)
	final, err := tea.NewProgram(model, programOpts...).Run()
	if err != nil {
		return "", fmt.Errorf("vault unlock splash: %w", err)
	}
	m, ok := final.(*vaultUnlockModel)
	if !ok || !m.done {
		return "", errVaultUnlockCancelled
	}
	return m.out, nil
}
