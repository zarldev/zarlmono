package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type turnSetupFailedMsg struct {
	Prompt string
	Error  string
}

// terminalNotice returns a transcript notice for a non-normal terminal
// reason, or "" for a clean completion. A turn that hit the iteration
// cap or was cancelled previously rendered identically to a finished
// answer (the event carried no reason); this makes the truncated /
// aborted state visible in the transcript.
func terminalNotice(reason runner.TerminalReason, iterations int) string {
	switch reason {
	case runner.TerminalMaxIterations:
		return palette.Warning.On(fmt.Sprintf("⚠ reached the iteration limit (%d) — the turn was cut off before the model finished", iterations))
	case runner.TerminalCancelled:
		return palette.Muted.On("■ turn cancelled")
	default:
		return ""
	}
}

// handleRunnerMsg is the single dispatch point for all runner events delivered
// through the sink's pump goroutine. Session owns cross-pane state mutation;
// this switch delegates those changes to Session and keeps the view-specific
// timeline/sub-agent DOM updates here. It returns true when msg was a runner
// event it consumed, so Update can early-return and keep its own switch focused
// on input/resize.
//
// Depth>0 events (spawn_agent sub-agents) route into a collapsible
// subAgentItem instead of rendering as flat indented notices.
func (m *UI) handleRunnerMsg(msg tea.Msg) (bool, tea.Cmd) {
	var cmd tea.Cmd
	switch e := msg.(type) {
	case turnSetupFailedMsg:
		effect := m.session.applyTurnSetupFailed(e)
		if effect.PromptToRender != "" {
			m.timeline.addUser(effect.PromptToRender)
		}
		if effect.ToastChanged {
			cmd = m.toastExpiryCmd()
		}

	case teasink.ConversationStartedMsg:
		effect := m.session.applyConversationStarted(e, time.Now())

		// --- Timeline / cockpit ---
		switch {
		case e.Depth == 0:
			m.timeline.closeGroups()
			if effect.PromptToRender != "" {
				m.timeline.addUser(effect.PromptToRender)
			}
			m.timeline.startTurn(e.TaskID, 0)
		case e.Depth > 0:
			m.timeline.startSubAgent(e.TaskID, e.Depth, e.AgentName, e.Prompt)
		}

	case teasink.ContentMsg:
		m.session.applyContent(e)
		m.timeline.appendContent(e.TaskID, e.Depth, e.Delta)

	case teasink.ThinkingMsg:
		m.session.applyThinking(e)
		m.timeline.appendThinking(e.TaskID, e.Depth, e.Delta)

	case teasink.ToolStartedMsg:
		m.session.applyToolStarted(e)
		m.timeline.startTool(e.TaskID, e.Depth, e.ToolID, e.ToolName, toolArgHint(e.ToolName, e.Parameters))
		m.notePRRelevantTool(e.ToolName, e.Parameters)

	case teasink.ToolCompletedMsg:
		effect := m.session.applyToolCompleted(e)
		m.timeline.finishTool(e.ToolID, e.FormattedResult, e.Result, e.Duration, false, tools.Kinds.UNKNOWN, effectSummaries(e.Effects)...)
		if effect.LoadedSkillName != "" {
			m.timeline.addLoadedSkill(e.TaskID, effect.LoadedSkillName)
		}

	case teasink.ToolFailedMsg:
		m.session.applyToolFailed(e)
		m.timeline.finishTool(e.ToolID, e.Error, nil, e.Duration, true, e.Kind, effectSummaries(e.Effects)...)

	case teasink.DiffMsg:
		m.session.applyDiff(e)
		m.timeline.addDiff(e.Path, e.Diff)

	case teasink.PlanUpdatedMsg:
		m.session.applyPlanUpdated(e)
		m.timeline.addPlanUpdate(e.Plan)

	case teasink.IterationCompletedMsg:
		m.session.applyIterationCompleted(e)

	case teasink.CompactionAppliedMsg:
		m.session.applyCompactionApplied(e)
		if e.Depth == 0 {
			m.timeline.attachCompaction(e.TaskID, compactionNotice(e.MessagesBefore, e.MessagesAfter, e.BytesTrimmed, e.Engine))
		}

	case teasink.SteerInjectedMsg:
		m.session.applySteerInjected(e)
		injected := 0
		if e.Depth == 0 {
			m.timeline.closeGroups()
			m.timeline.endTurn(e.TaskID)
		}
		for _, msg := range e.Messages {
			if msg.Role != "user" || strings.TrimSpace(msg.Content) == "" {
				continue
			}
			injected++
			if e.Depth == 0 {
				m.timeline.addInjectedUser(msg.Content)
				continue
			}
			if sa := m.timeline.subAgent(e.TaskID); sa != nil {
				sa.addNotice("↳ injected: " + firstLine(msg.Content))
			}
		}
		if e.Depth == 0 && injected > 0 {
			m.timeline.startTurn(e.TaskID, 0)
		}

	case teasink.ConversationEndedMsg:
		// One terminal event for every outcome; branch on Reason. An error
		// surfaces a failure notice/toast; any other reason (completed /
		// max-iter / cancelled) gets the normal end-of-turn treatment plus
		// a notice for the non-clean reasons.
		effect := m.session.applyConversationEnded(e, time.Now())
		failed := e.Reason == runner.TerminalError
		if e.Depth > 0 {
			if sa := m.timeline.subAgent(e.TaskID); sa != nil {
				if failed {
					sa.addNotice("✗ " + e.Error)
				} else if notice := terminalNotice(e.Reason, e.Iterations); notice != "" {
					sa.addNotice(notice)
				}
			}
			m.timeline.finishSubAgent(e.TaskID)
		} else {
			m.timeline.endTurn(e.TaskID)
			if failed {
				m.timeline.closeGroups()
				if effect.ToastChanged {
					cmd = m.toastExpiryCmd()
				}
			} else {
				if notice := terminalNotice(e.Reason, e.Iterations); notice != "" {
					m.timeline.addNotice(notice)
				}
				m.timeline.closeGroups()
				cmd = tea.Batch(cmd, m.launchQueuedTurn())
			}
			// Re-resolve the PR after the turn settles: catches an agent
			// checkout (branch change) or a git/gh tool that opened/pushed a PR.
			cmd = tea.Batch(cmd, m.refreshPRCmd())
		}

	default:
		return false, nil
	}
	return true, cmd
}
