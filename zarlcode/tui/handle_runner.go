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
// Depth>0 events (agent_spawn sub-agents) route into a collapsible
// subAgentItem instead of rendering as flat indented notices.
func (m *UI) handleRunnerMsg(msg tea.Msg) (bool, tea.Cmd) {
	var cmd tea.Cmd
	switch e := msg.(type) {
	case turnSetupFailedMsg:
		effect := m.session.applyTurnSetupFailed(e)
		if effect.PromptToRender != "" || len(effect.Attachments) > 0 {
			m.timeline.addUserWithAttachments(effect.PromptToRender, effect.Attachments)
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
			if effect.PromptToRender != "" || len(effect.Attachments) > 0 {
				m.timeline.addUserWithAttachments(effect.PromptToRender, effect.Attachments)
			}
			m.timeline.startTurn(e.TaskID, 0)
		case e.Depth > 0:
			agentName := e.AgentName
			if agentName == "" {
				agentName = "agent"
			}
			m.timeline.startSubAgentWithParent(e.TaskID, e.Depth, agentName, e.Provider, e.Model, e.Prompt, e.ParentToolCallID)
		}

	case teasink.ContentMsg:
		m.session.applyContent(e)
		m.timeline.appendContent(e.TaskID, e.Depth, e.Delta)

	case teasink.ThinkingMsg:
		m.session.applyThinking(e)
		m.timeline.appendThinking(e.TaskID, e.Depth, e.Delta)

	case teasink.ToolStartedMsg:
		m.session.applyToolStarted(e)
		m.timeline.startToolWithParent(e.TaskID, e.Depth, e.ToolID, e.ToolName, toolArgHint(e.ToolName, e.Parameters), e.ParentToolID, e.Sequence)
		if e.ToolName == "agent_spawn" {
			agent, _ := e.Parameters["agent"].(string)
			if agent == "" {
				agent = "agent"
			}
			prompt, _ := e.Parameters["prompt"].(string)
			m.timeline.reserveSubAgent(e.ToolID, e.Depth, agent, prompt)
		}
		m.notePRRelevantTool(e.ToolName, e.Parameters)
	case teasink.WorkspaceWaitStartedMsg:
		m.session.applyWorkspaceWaitStarted(e)
		m.timeline.waitTool(e.ToolID, e.Access, e.Paths)

	case teasink.WorkspaceWaitEndedMsg:
		m.session.applyWorkspaceWaitEnded(e)
		m.timeline.resumeTool(e.ToolID, e.Duration)

	case teasink.ToolCompletedMsg:
		effect := m.session.applyToolCompleted(e)
		m.timeline.finishTool(e.ToolID, e.FormattedResult, e.Result, e.Duration, false, tools.Kinds.UNKNOWN, effectSummaries(e.Effects)...)
		if effect.LoadedSkillName != "" {
			m.timeline.addLoadedSkill(e.TaskID, effect.LoadedSkillName)
		}

	case teasink.ToolFailedMsg:
		m.session.applyToolFailed(e)
		m.timeline.finishTool(e.ToolID, e.Error, nil, e.Duration, true, e.Kind, effectSummaries(e.Effects)...)
		if e.ToolName == "agent_spawn" {
			m.timeline.failSubAgentSpawn(e.ToolID, e.Error)
		}

	case teasink.DiffMsg:
		m.session.applyDiff(e)
		m.timeline.addDiff(e.TaskID, e.Path, e.Diff)

	case teasink.PlanUpdatedMsg:
		before := m.session.Plan
		m.session.applyPlanUpdated(e)
		m.timeline.addPlanUpdate(e.TaskID, e.Plan)
		cmd = tea.Batch(cmd, planProgressSoundCmd(m.appContext(), m.settings, before, e.Plan))

	case teasink.PromptDiagnosticsMsg:
		for _, diag := range e.Diagnostics {
			if strings.TrimSpace(diag) == "" {
				continue
			}
			m.timeline.addNotice(palette.Warning.On("⚠ " + diag))
			m.session.SetToastTone(diag, toastInfo)
			cmd = tea.Batch(cmd, m.toastExpiryCmd())
		}

	case teasink.IterationCompletedMsg:
		m.session.applyIterationCompleted(e)

	case teasink.CompactionAppliedMsg:
		m.session.applyCompactionApplied(e)
		if e.Depth == 0 {
			notice := compactionNotice(e.MessagesBefore, e.MessagesAfter, e.BytesTrimmed, e.Engine)
			m.session.SetToastTone(notice, toastInfo)
			cmd = tea.Batch(cmd, m.toastExpiryCmd())
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
				m.timeline.addNoticeForTurn(e.TaskID, "↳ injected: "+firstLine(msg.Content))
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
		if !effect.Accepted {
			return true, nil
		}
		failed := e.Reason == runner.TerminalError
		if e.Depth > 0 {
			if sa := m.timeline.subAgent(e.TaskID); sa != nil {
				if failed {
					detail := userFacingProviderError(e.Error)
					if e.RateLimit != nil {
						detail = formatRateLimit(e.RateLimit)
					}
					m.timeline.addNoticeForTurn(e.TaskID, "✗ "+detail)
				} else if notice := terminalNotice(e.Reason, e.Iterations); notice != "" {
					m.timeline.addNoticeForTurn(e.TaskID, notice)
				}
			}
			terminalFailed := failed || e.Reason == runner.TerminalMaxIterations
			m.timeline.finishSubAgent(e.TaskID, terminalFailed, e.Reason == runner.TerminalCancelled)
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
			if !failed && e.Reason == runner.TerminalCompleted {
				cmd = tea.Batch(cmd, completionSoundCmd(m.appContext(), m.settings))
			}
			cmd = tea.Batch(cmd, m.refreshPRCmd())
		}

	default:
		return false, nil
	}
	return true, cmd
}
