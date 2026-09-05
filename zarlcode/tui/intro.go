package tui

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// introLogoLarge is the full ZARLCODE banner; introLogoSmall is the compact
// wordmark shown when the terminal is too narrow for the banner.
//
//go:embed logo.txt
var introLogoLarge string

// logoWidth is the display width of the widest line in a multi-line logo.

type introFocus int

const (
	introFocusPrompt introFocus = iota
	introFocusSessions
)

type introPane struct {
	wsRoot      string
	sessions    []sessionSummary
	cursor      int
	focus       introFocus
	searching   bool
	searchQuery []rune
	matches     []int
	renaming    bool
	prompt      []rune
	pos         int
	err         string
	provider    string
	model       string
}

func newIntroPane(wsRoot string, sessions []sessionSummary, provider, model string) *introPane {
	p := &introPane{
		wsRoot:   wsRoot,
		sessions: sessions,
		focus:    introFocusPrompt,
		provider: provider,
		model:    model,
	}
	p.sortSessions()
	return p
}

func (p *introPane) handleKey(m *UI, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "tab", "shift+tab":
		if len(p.sessions) > 0 {
			if p.focus == introFocusPrompt {
				p.focus = introFocusSessions
			} else {
				p.focus = introFocusPrompt
			}
		}
		return nil
	}

	if p.searching {
		return p.handleSearchKey(m, msg)
	}
	if p.renaming {
		return p.handleRenameKey(m, msg)
	}
	if msg.String() == "ctrl+n" {
		if session, ok := p.selectedSession(); ok {
			p.focus = introFocusSessions
			p.renaming = true
			p.prompt = []rune(session.Label)
			p.pos = len(p.prompt)
			p.err = ""
		}
		return nil
	}
	if p.focus == introFocusPrompt {
		return p.handlePromptKey(m, msg)
	}
	return p.handleSessionKey(m, msg)
}

// handlePaste inserts pasted/clipboard text into the prompt field.
func (p *introPane) handlePaste(content string) {
	if p.focus == introFocusPrompt || p.renaming {
		p.insert(content)
	} else if p.searching {
		p.insertSearch(content)
	}
}

func (p *introPane) handlePromptKey(m *UI, msg tea.KeyPressMsg) tea.Cmd {
	if isMultilineInsertKey(msg) {
		p.insert("\n")
		return nil
	}
	switch msg.String() {
	case "enter":
		return m.dismissIntroFresh(strings.TrimSpace(string(p.prompt)))
	case "esc":
		p.err = ""
	case "backspace":
		p.backspace()
	case "delete":
		if p.pos < len(p.prompt) {
			p.prompt = append(p.prompt[:p.pos], p.prompt[p.pos+1:]...)
		}
	case "left":
		if p.pos > 0 {
			p.pos--
		}
	case "right":
		if p.pos < len(p.prompt) {
			p.pos++
		}
	case "home":
		p.pos = 0
	case "end":
		p.pos = len(p.prompt)
	default:
		if msg.Text != "" {
			p.insert(msg.Text)
		}
	}
	return nil
}

func isMultilineInsertKey(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	if k.Code == tea.KeyEnter || k.Code == tea.KeyReturn || k.Code == tea.KeyKpEnter {
		return k.Mod&tea.ModShift != 0 || k.Mod&tea.ModAlt != 0 || k.Mod&tea.ModCtrl != 0
	}
	return msg.String() == "ctrl+j"
}

func (p *introPane) handleSessionKey(m *UI, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "/":
		p.searching = true
		p.searchQuery = nil
		p.refreshMatches("")
	case "p":
		return p.toggleSelectedSessionPin(m)
	case "ctrl+n":
		if session, ok := p.selectedSession(); ok {
			p.renaming = true
			p.prompt = []rune(session.Label)
			p.pos = len(p.prompt)
			p.err = ""
		}
	case "enter":
		if session, ok := p.selectedSession(); ok {
			return m.resumeIntroSession(session.ID)
		}
	case "esc":
		p.err = ""
		p.focus = introFocusPrompt
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.matches)-1 {
			p.cursor++
		}
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		if len(p.matches) > 0 {
			p.cursor = len(p.matches) - 1
		}
	case "pgup", "ctrl+u":
		p.cursor -= introVisibleSessions
		if p.cursor < 0 {
			p.cursor = 0
		}
	case "d", "delete":
		if session, ok := p.selectedSession(); ok {
			m.overlay.push(newDeleteSessionConfirmDialog(session.ID, introSessionDisplayLabel(session)))
		}
	case "pgdown", "ctrl+d":
		if len(p.matches) > 0 {
			p.cursor += introVisibleSessions
			if p.cursor >= len(p.matches) {
				p.cursor = len(p.matches) - 1
			}
		}
	}
	return nil
}

func (p *introPane) toggleSelectedSessionPin(m *UI) tea.Cmd {
	session, ok := p.selectedSession()
	if !ok {
		return nil
	}
	p.err = ""
	return m.setIntroSessionPinned(session.ID, !session.Pinned)
}

func (p *introPane) handleRenameKey(m *UI, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c", "ctrl+n":
		p.renaming = false
		p.prompt = nil
		p.pos = 0
	case "enter":
		session, ok := p.selectedSession()
		if !ok {
			return nil
		}
		label := normalizeSessionLabel(string(p.prompt))
		p.renaming = false
		p.prompt = nil
		p.pos = 0
		return m.renameIntroSession(session.ID, label)
	case "backspace":
		p.backspace()
	case "delete":
		if p.pos < len(p.prompt) {
			p.prompt = append(p.prompt[:p.pos], p.prompt[p.pos+1:]...)
		}
	case "left":
		if p.pos > 0 {
			p.pos--
		}
	case "right":
		if p.pos < len(p.prompt) {
			p.pos++
		}
	case "home":
		p.pos = 0
	case "end":
		p.pos = len(p.prompt)
	default:
		if msg.Text != "" && len(p.prompt) < maxSessionLabelRunes {
			runes := []rune(msg.Text)
			if remaining := maxSessionLabelRunes - len(p.prompt); len(runes) > remaining {
				runes = runes[:remaining]
			}
			p.insert(string(runes))
		}
	}
	return nil
}

func (p *introPane) handleSearchKey(m *UI, msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		p.searching = false
		p.searchQuery = nil
		p.refreshMatches("")
	case "enter":
		if session, ok := p.selectedSession(); ok {
			return m.resumeIntroSession(session.ID)
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
		if len(p.searchQuery) > 0 {
			p.searchQuery = p.searchQuery[:len(p.searchQuery)-1]
			p.refreshMatches(string(p.searchQuery))
		}
	default:
		if msg.Text != "" {
			p.insertSearch(msg.Text)
		}
	}
	return nil
}

func (p *introPane) insert(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	rs := []rune(s)
	out := make([]rune, 0, len(p.prompt)+len(rs))
	out = append(out, p.prompt[:p.pos]...)
	out = append(out, rs...)
	out = append(out, p.prompt[p.pos:]...)
	p.prompt = out
	p.pos += len(rs)
}

func (p *introPane) backspace() {
	if p.pos == 0 {
		return
	}
	p.prompt = append(p.prompt[:p.pos-1], p.prompt[p.pos:]...)
	p.pos--
}

const introVisibleSessions = 7

func (p *introPane) draw(scr uv.Screen, area uv.Rectangle, planMode bool) {
	w, h := area.Dx(), area.Dy()
	if w <= 0 || h <= 0 {
		return
	}
	logoCol := palette.Primary
	if planMode {
		logoCol = palette.PlanMode
	}

	// --- info block: lines share a common left edge, block centered ---
	var infoBlock []string
	if p.err != "" {
		infoBlock = append(infoBlock, palette.Error.On("session: "+p.err), "")
	}
	infoBlock = append(infoBlock, p.promptLines(w, planMode)...)
	if m := p.modelLine(); m != "" {
		infoBlock = append(infoBlock, m)
	}
	if m := p.activeLine(); m != "" {
		infoBlock = append(infoBlock, m)
	}
	infoBlock = append(infoBlock, "")
	infoBlock = append(infoBlock, p.sessionLines()...)
	infoBlock = append(infoBlock, "")
	infoBlock = append(infoBlock, p.footer())
	drawSplash(scr, area, logoCol, infoBlock)
}

func (p *introPane) activeLine() string {
	return palette.Muted.On("workspace  ") + palette.Subtle.On(p.wsRoot)
}

func (p *introPane) modelLine() string {
	if p.provider == "" && p.model == "" {
		return ""
	}
	label := palette.Muted.On("model     ")
	detail := p.provider
	if p.model != "" {
		if detail != "" {
			detail += " / "
		}
		detail += p.model
	}
	if detail == "" {
		return ""
	}
	return label + palette.Subtle.On(detail)
}

func (p *introPane) promptLines(width int, planMode bool) []string {
	border := palette.Border
	accent := palette.Primary
	placeholder := "What are we building?"
	if planMode {
		border = palette.PlanMode
		accent = palette.PlanMode
		placeholder = "What should we plan?"
	}
	if p.focus == introFocusPrompt {
		border = accent
	}
	return splashPromptBoxLines(width, border, accent, func(textWidth int) []string {
		return p.promptDisplayLines(accent, placeholder, textWidth)
	})
}

const introPromptMaxLines = 6

func (p *introPane) promptDisplayLines(accent theme.Color, placeholder string, width int) []string {
	if len(p.prompt) == 0 {
		return []string{palette.Muted.On(placeholder)}
	}
	// Build logical lines (split on \n) with an unstyled cursor marker
	// inserted at the cursor position. Wrapping happens after — tracking
	// the cursor display line via the unstyled marker and styling later
	// avoids any ANSI-width interference with lipgloss wrapping.
	var logical []string
	var b strings.Builder
	for i, r := range p.prompt {
		if p.focus == introFocusPrompt && i == p.pos {
			b.WriteRune('▏')
		}
		if r == '\n' {
			logical = append(logical, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if p.focus == introFocusPrompt && p.pos == len(p.prompt) {
		b.WriteRune('▏')
	}
	logical = append(logical, b.String())

	// Wrap each logical line at the inner box width.
	var display []string
	for _, line := range logical {
		wrapped := wrapText(line, width)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		display = append(display, wrapped...)
	}

	// Find which display line holds the cursor marker.
	cursorDisplay := -1
	if p.focus == introFocusPrompt {
		for i, line := range display {
			if strings.ContainsRune(line, '▏') {
				cursorDisplay = i
				break
			}
		}
	}

	// Style the cursor marker now that wrapping is settled.
	for i := range display {
		display[i] = strings.ReplaceAll(display[i], "▏", accent.On("▏"))
	}

	if len(display) <= introPromptMaxLines {
		return display
	}
	if cursorDisplay < 0 {
		return display[:introPromptMaxLines]
	}
	start := max(cursorDisplay-introPromptMaxLines+1, 0)
	if start+introPromptMaxLines > len(display) {
		start = len(display) - introPromptMaxLines
	}
	return display[start : start+introPromptMaxLines]
}

func padStyled(s string, width int) string {
	if pad := width - ansi.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func (p *introPane) sessionLines() []string {
	head := "sessions"
	if p.renaming {
		head = "rename session"
	} else if p.searching {
		head = "search sessions: " + string(p.searchQuery) + "▏"
	}
	if len(p.sessions) == 0 {
		return []string{palette.Subtle.On(head), palette.Muted.On("  (none yet — type above to start fresh)")}
	}
	if len(p.matches) == 0 {
		return []string{palette.Subtle.On(head), palette.Muted.On("  No matching sessions · esc clear")}
	}
	if len(p.matches) > introVisibleSessions {
		head += fmt.Sprintf(" [ %d/%d ]", p.cursor+1, len(p.matches))
	}
	out := []string{palette.Subtle.On(head)}
	start := 0
	if p.cursor >= introVisibleSessions {
		start = p.cursor - introVisibleSessions + 1
	}
	end := min(start+introVisibleSessions, len(p.matches))
	for displayIndex := start; displayIndex < end; displayIndex++ {
		s := p.sessions[p.matches[displayIndex]]
		label := introSessionDisplayLabel(s)
		if p.renaming && displayIndex == p.cursor {
			label = string(p.prompt)
			if label == "" {
				label = "(unnamed)"
			}
			label += "▏"
		}
		if s.Pinned {
			label = "★ " + label
		}
		label = truncateRunes(label, 42)

		metadata := introSessionMetadata(s)
		row := fmt.Sprintf("%-42s  %s", label, metadata)
		if displayIndex == p.cursor && p.focus == introFocusSessions {
			out = append(out, palette.Primary.On("▶ "+row))
		} else {
			out = append(out, palette.Subtle.On("  "+row))
		}
	}
	return out
}

func introSessionMetadata(session sessionSummary) string {
	parts := make([]string, 0, 6)
	if !session.SavedAt.IsZero() {
		parts = append(parts, formatAgo(time.Since(session.SavedAt)))
	} else {
		parts = append(parts, "saved")
	}
	if session.AgentName != "" {
		parts = append(parts, session.AgentName)
	}
	if session.Model != "" {
		parts = append(parts, session.Model)
	}
	if session.HasDraft {
		parts = append(parts, "Draft")
	}
	parts = append(parts, fmt.Sprintf("%d msgs", session.Messages))
	if session.PlanTotalCount > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d plan", session.PlanCompletedCount, session.PlanTotalCount))
	}
	if session.ChangedFileCount > 0 {
		parts = append(parts, fmt.Sprintf("%d files", session.ChangedFileCount))
	}
	return strings.Join(parts, " · ")
}

func (p *introPane) selectedSession() (sessionSummary, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return sessionSummary{}, false
	}
	index := p.matches[p.cursor]
	if index < 0 || index >= len(p.sessions) {
		return sessionSummary{}, false
	}
	return p.sessions[index], true
}

func (p *introPane) refreshMatches(query string) {
	selectedID := ""
	if selected, ok := p.selectedSession(); ok {
		selectedID = selected.ID
	}
	p.refreshMatchesPreserving(query, selectedID)
}

func (p *introPane) refreshMatchesPreserving(query, selectedID string) {
	documents := make([]searchDocument, len(p.sessions))
	for index, session := range p.sessions {
		documents[index] = searchDocument{ID: session.ID, Fields: []string{session.Label, session.AgentName, session.Model, session.ID}}
	}
	p.matches = searchDocuments(documents, query)
	p.cursor = preserveSearchSelection(documents, p.matches, selectedID)
}

func (p *introPane) sortSessions() {
	selectedID := ""
	if selected, ok := p.selectedSession(); ok {
		selectedID = selected.ID
	}
	sort.SliceStable(p.sessions, func(i, j int) bool {
		left, right := p.sessions[i], p.sessions[j]
		if left.Pinned != right.Pinned {
			return left.Pinned
		}
		if left.Pinned && !left.PinnedAt.Equal(right.PinnedAt) {
			return left.PinnedAt.After(right.PinnedAt)
		}
		return left.SavedAt.After(right.SavedAt)
	})
	p.refreshMatchesPreserving(string(p.searchQuery), selectedID)
}

func (p *introPane) insertSearch(text string) {
	text = strings.ReplaceAll(text, "\n", " ")
	p.searchQuery = append(p.searchQuery, []rune(text)...)
	p.refreshMatches(string(p.searchQuery))
}

func introSessionDisplayLabel(session sessionSummary) string {
	if label := strings.TrimSpace(session.Label); label != "" {
		return label
	}
	if !session.CreatedAt.IsZero() {
		return session.CreatedAt.Format("2006-01-02 15:04")
	}
	return "Unnamed session"
}

func (p *introPane) footer() string {
	key := func(k string) string { return palette.Subtle.On(k) }
	mut := func(s string) string { return palette.Muted.On(s) }
	if p.focus == introFocusSessions {
		if p.renaming {
			return key("enter") + mut(" save name") + mut("    ") + key("esc") + mut(" cancel")
		}
		return key("↑↓") + mut(" pick") + mut("    ") + key("enter") + mut(" resume") + mut("    ") + key("p") + mut(" pin") + mut("    ") + key("/") + mut(" search") + mut("    ") + key("ctrl+n") + mut(" rename") + mut("    ") + key("tab") + mut(" prompt")
	}
	return key("enter") + mut(" start") + mut("    ") + key("tab") + mut(" sessions") + mut("    ") + key("ctrl+g") + mut(" keys")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
