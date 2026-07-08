package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// liveTurnFinishedMsg is emitted by RunFn once a turn's conversation history is
// committed. Runner ConversationEnded events fire before that commit, so a
// save driven from the event alone can miss the just-finished turn.
type liveTurnFinishedMsg struct{}

// RunFn is the UI.SetRunFn handler: it adapts the charm-free
// [engine.LiveRunner.RunTurn] into a tea.Cmd. A setup failure surfaces as
// turnSetupFailedMsg; a finished turn surfaces as liveTurnFinishedMsg. The turn
// runs off the Update loop, and streaming output reaches the timeline through
// the sink's pump. It is a package func, not a method, because RunFn must live
// in the TUI (it returns a tea.Cmd) while LiveRunner lives in the engine.
func RunFn(l *engine.LiveRunner, prompt string) tea.Cmd {
	return RunFnWithAttachments(l, prompt, nil)
}

func RunFnWithAttachments(l *engine.LiveRunner, prompt string, attachments []llm.ContentPart) tea.Cmd {
	return func() tea.Msg {
		if err := l.RunTurnWithAttachments(prompt, attachments); err != nil {
			return turnSetupFailedMsg{Prompt: prompt, Error: err.Error()}
		}
		return liveTurnFinishedMsg{}
	}
}
