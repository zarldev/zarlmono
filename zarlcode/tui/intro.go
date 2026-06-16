package tui

import (
	_ "embed"
	"fmt"
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
	wsRoot   string
	sessions []sessionSummary
	cursor   int
	focus    introFocus
	prompt   []rune
	pos      int
	err      string
	provider string
	model    string
}

func newIntroPane(wsRoot string, sessions []sessionSummary, provider, model string) *introPane {
	return &introPane{
		wsRoot:   wsRoot,
		sessions: sessions,
		focus:    introFocusPrompt,
		provider: provider,
		model:    model,
	}
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

	if p.focus == introFocusPrompt {
		return p.handlePromptKey(m, msg)
	}
	return p.handleSessionKey(m, msg)
}

// handlePaste inserts pasted/clipboard text into the prompt field.
func (p *introPane) handlePaste(content string) {
	if p.focus == introFocusPrompt {
		p.insert(content)
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
	case "enter":
		if len(p.sessions) > 0 {
			return m.resumeIntroSession(p.sessions[p.cursor].ID)
		}
	case "esc":
		p.err = ""
		p.focus = introFocusPrompt
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.sessions)-1 {
			p.cursor++
		}
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		if len(p.sessions) > 0 {
			p.cursor = len(p.sessions) - 1
		}
	case "pgup", "ctrl+u":
		p.cursor -= introVisibleSessions
		if p.cursor < 0 {
			p.cursor = 0
		}
	case "pgdown", "ctrl+d":
		if len(p.sessions) > 0 {
			p.cursor += introVisibleSessions
			if p.cursor >= len(p.sessions) {
				p.cursor = len(p.sessions) - 1
			}
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

func (p *introPane) paste(s string) {
	if p.focus == introFocusPrompt && s != "" {
		p.insert(s)
	}
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
	start := cursorDisplay - introPromptMaxLines + 1
	if start < 0 {
		start = 0
	}
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
	if len(p.sessions) == 0 {
		return []string{palette.Subtle.On(head), palette.Muted.On("  (none yet — type above to start fresh)")}
	}
	if len(p.sessions) > introVisibleSessions {
		head += fmt.Sprintf(" [ %d/%d ]", p.cursor+1, len(p.sessions))
	}
	out := []string{palette.Subtle.On(head)}
	start := 0
	if p.cursor >= introVisibleSessions {
		start = p.cursor - introVisibleSessions + 1
	}
	end := start + introVisibleSessions
	if end > len(p.sessions) {
		end = len(p.sessions)
	}
	for i := start; i < end; i++ {
		s := p.sessions[i]
		label := truncateRunes(s.Label, 42)
		meta := ""
		if !s.SavedAt.IsZero() {
			meta = formatAgo(time.Since(s.SavedAt))
		}
		if meta == "" {
			meta = "saved"
		}
		if s.Model != "" {
			meta += " · " + s.Model
		}
		row := fmt.Sprintf("%-42s  %s · %d msgs", label, meta, s.Messages)
		if i == p.cursor && p.focus == introFocusSessions {
			out = append(out, palette.Primary.On("▶ "+row))
		} else {
			out = append(out, palette.Subtle.On("  "+row))
		}
	}
	return out
}

func (p *introPane) footer() string {
	key := func(k string) string { return palette.Subtle.On(k) }
	mut := func(s string) string { return palette.Muted.On(s) }
	// Navigation hint is always present (either styled or as same-width
	// spaces) so the footer line width stays constant between focus states.
	// Without this the entire centered info block shifts horizontally when
	// tabbing between prompt and sessions.
	nav := key("↑↓") + mut(" pick")
	if p.focus != introFocusSessions {
		nav = strings.Repeat(" ", ansi.StringWidth(nav))
	}
	parts := append([]string{nav},
		key("tab")+mut(" focus"),
		key("ctrl+p")+mut(" plan pane"),
		key("ctrl+s")+mut(" settings"),
		key("ctrl+t")+mut(" theme"),
		key("ctrl+g")+mut(" keys"),
		key("ctrl+c")+mut(" quit"),
	)
	return strings.Join(parts, mut("    "))
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
