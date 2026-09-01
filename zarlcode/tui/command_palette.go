package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type commandPaletteEntry struct {
	id          CommandID
	label       string
	description string
	aliases     []string
	shortcut    string
	available   func(*UI) bool
	run         func(*UI) tea.Cmd
}

var commandPaletteEntries = []commandPaletteEntry{
	{id: CommandIDs.COMMANDHELP, label: "Open help", description: "Show keyboard shortcuts", aliases: []string{"keys", "shortcuts"}, shortcut: "ctrl+g", run: func(m *UI) tea.Cmd { m.overlay.push(m.newHelpDialog()); return nil }},
	{id: CommandIDs.COMMANDSETTINGS, label: "Open settings", description: "Configure zarlcode", aliases: []string{"preferences"}, shortcut: "ctrl+s", available: func(m *UI) bool { return m.settings != nil }, run: func(m *UI) tea.Cmd { return m.openSettings() }},
	{id: CommandIDs.COMMANDTHEME, label: "Choose theme", description: "Change the interface theme", aliases: []string{"appearance", "colors"}, shortcut: "ctrl+t", run: func(m *UI) tea.Cmd { m.overlay.push(newThemePicker()); return nil }},
	{id: CommandIDs.COMMANDMODELS, label: "Choose model", description: "Switch provider and model", aliases: []string{"provider"}, shortcut: "ctrl+e", run: func(m *UI) tea.Cmd { return m.openModelQuickPick() }},
	{id: CommandIDs.COMMANDNAMESESSION, label: "Name session", description: "Set a memorable session name", aliases: []string{"rename", "title"}, shortcut: "ctrl+n", run: func(m *UI) tea.Cmd { m.openSessionNameDialog(); return nil }},
	{id: CommandIDs.COMMANDPLAN, label: "Open plan", description: "Inspect the current plan", aliases: []string{"steps"}, shortcut: "ctrl+p", run: func(m *UI) tea.Cmd {
		m.overlay.push(newPlanDialog(&m.session.Plan, m.session.WorkspaceDir))
		return nil
	}},
	{id: CommandIDs.COMMANDTOOLHISTORY, label: "Open tool history", description: "Inspect prior tool calls", aliases: []string{"tools"}, shortcut: "ctrl+h", available: func(m *UI) bool { return m.settings != nil }, run: func(m *UI) tea.Cmd {
		m.overlay.push(newToolHistory(m.appContext(), m.settings.Store, m.session.ID))
		return nil
	}},
	{id: CommandIDs.COMMANDFILES, label: "Open files", description: "Browse workspace files", aliases: []string{"workspace", "tree"}, shortcut: "ctrl+f", run: func(m *UI) tea.Cmd {
		viewer := newFileViewer(m.appContext(), m.session.WorkspaceDir)
		m.overlay.push(viewer)
		return m.fileViewerInitialCmd(viewer)
	}},
	{id: CommandIDs.COMMANDCOPYLASTRESPONSE, label: "Copy last response", description: "Copy the most recent assistant response", aliases: []string{"clipboard", "assistant", "answer"}, shortcut: "ctrl+shift+c", run: func(m *UI) tea.Cmd { return m.copyLastAssistantResponse() }},
	{id: CommandIDs.COMMANDEXPORTSESSION, label: "Export session", description: "Write this conversation as Markdown", aliases: []string{"markdown", "save"}, run: func(m *UI) tea.Cmd { return m.exportSession("") }},
}

type commandPalette struct {
	entries []commandPaletteEntry
	matches []int
	query   []rune
	cursor  int
}

func newCommandPalette(m *UI) *commandPalette {
	p := &commandPalette{}
	for _, entry := range commandPaletteEntries {
		if entry.available == nil || entry.available(m) {
			p.entries = append(p.entries, entry)
		}
	}
	p.filter()
	return p
}

func (p *commandPalette) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+k":
		return actionClose{}
	case "enter":
		if p.cursor >= 0 && p.cursor < len(p.matches) {
			return actionRunCommand{id: p.entries[p.matches[p.cursor]].id}
		}
	case "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down":
		if p.cursor < len(p.matches)-1 {
			p.cursor++
		}
	case "backspace":
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.filter()
		}
	default:
		if msg.Text != "" {
			p.query = append(p.query, []rune(strings.ReplaceAll(msg.Text, "\n", " "))...)
			p.filter()
		}
	}
	return actionNone{}
}

func (p *commandPalette) handlePaste(text string) {
	p.query = append(p.query, []rune(strings.ReplaceAll(text, "\n", " "))...)
	p.filter()
}

func (p *commandPalette) filter() {
	documents := make([]searchDocument, len(p.entries))
	for index, entry := range p.entries {
		documents[index] = searchDocument{ID: entry.id.String(), Fields: append([]string{entry.label, entry.description}, entry.aliases...)}
	}
	p.matches = searchDocuments(documents, string(p.query))
	p.cursor = 0
}

func (p *commandPalette) draw(scr uv.Screen, area uv.Rectangle) {
	lines := []string{palette.Primary.On("› ") + string(p.query) + palette.Primary.On("▏")}
	if len(p.matches) == 0 {
		lines = append(lines, palette.Muted.On("No matching commands"))
	}
	for displayIndex, match := range p.matches {
		entry := p.entries[match]
		row := entry.label
		if entry.shortcut != "" {
			row += "  " + palette.Muted.On(entry.shortcut)
		}
		if displayIndex == p.cursor {
			row = palette.Primary.On("▶ " + row)
		} else {
			row = "  " + row
		}
		lines = append(lines, row, palette.Muted.On("    "+entry.description))
	}
	drawActionDialog(scr, area, "command palette", "search actions", lines, keyLegend(keyHint{"enter", "run"}, keyHint{"esc", "close"}), 82)
}

func commandEntry(id CommandID) (commandPaletteEntry, bool) {
	for _, entry := range commandPaletteEntries {
		if entry.id == id {
			return entry, true
		}
	}
	return commandPaletteEntry{}, false
}

func (m *UI) openCommandPalette() { m.overlay.push(newCommandPalette(m)) }
