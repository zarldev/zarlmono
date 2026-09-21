package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

// modeItem keeps applied workflow changes with the turn's supporting activity.
// Reasons remain available on disclosure rather than interrupting the answer.
type modeItem struct {
	versioned
	mode     string
	reason   string
	expanded bool
}

func (m *modeItem) finished() bool { return true }

func (m *modeItem) toggle() {
	m.expanded = !m.expanded
	m.bump()
}

func (m *modeItem) togglerAt(_, line int) toggler {
	if line == 0 {
		return m
	}
	return nil
}

func (m *modeItem) render(width int) []string {
	glyph := "[+]"
	if m.expanded {
		glyph = "[-]"
	}
	lines := renderPlain(width, glyph+" "+strings.ToLower(m.mode)+" mode", withStyle(palette.Muted.On))
	if m.expanded {
		lines = append(lines, renderPlain(width, m.reason,
			withFirstPrefix("  ", "  "), withStyle(palette.Subtle.On))...)
	}
	return lines
}

func (tl *timeline) addModeChange(taskID, mode, reason string) {
	turn := tl.ensureTurn(taskID, 0)
	// Retain the existing durable notice representation for resume/export.
	tl.applyTranscript(transcript.NoticeAdded{TurnID: taskID, Text: mode + ": " + reason})
	tl.markTurnActivity(turn)
	think := tl.ensureThinking(turn)
	think.children = append(think.children, &modeItem{mode: mode, reason: reason})
	think.bump()
	tl.invalidateItem(think)
}

func restoredModeNotice(text string) (*modeItem, bool) {
	// Older mode notices included presentation ANSI in the same durable text.
	// Recognize only the two historical host notice labels, not user messages.
	mode, reason, found := strings.Cut(ansi.Strip(text), ": ")
	if found && (mode == "Plan" || mode == "Build") {
		return &modeItem{mode: mode, reason: reason}, true
	}
	return nil, false
}
