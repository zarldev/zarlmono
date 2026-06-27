package tui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type setupFailedEffect struct {
	PromptToRender string
	ToastChanged   bool
}

type conversationStartedEffect struct {
	PromptToRender string
}

type toolCompletedEffect struct {
	LoadedSkillName string
}

type sessionEffect struct {
	ToastChanged bool
	Notice       string
}

func (s *Session) applyTurnSetupFailed(e turnSetupFailedMsg) setupFailedEffect {
	s.logEvent("run setup failed", e.Error)
	s.SetErrorToast("setup: " + e.Error)
	s.Run.Running = false
	return setupFailedEffect{PromptToRender: trimPromptForNotice(e.Prompt), ToastChanged: true}
}

func (s *Session) applyConversationStarted(e teasink.ConversationStartedMsg, now time.Time) conversationStartedEffect {
	s.LastParentToolCallID = e.ParentToolCallID
	s.LastAgentName = e.AgentName
	s.logEvent("run started", e.TaskID)

	switch {
	case e.Depth == 0:
		ordinal := s.workingSet().StartTurn(e.TaskID)
		s.checkpoints().StartTurn(e.TaskID, ordinal, now)
		s.Run.reset()
		s.Run.Running = true
	case e.Depth > 0:
		if e.Depth > s.Run.maxDepth {
			s.Run.maxDepth = e.Depth
		}
	}

	return conversationStartedEffect{PromptToRender: s.consumeStartedPrompt(e.Prompt)}
}

func (s *Session) applyContent(e teasink.ContentMsg) {
	if e.Depth == 0 {
		s.Run.turnOutBytes += len(e.Delta)
	}
}

func (s *Session) applyThinking(e teasink.ThinkingMsg) {
	// Reasoning tokens are billed/generated like output, so they count toward
	// the top-level turn's throughput. The text itself is a view concern,
	// rendered by the timeline's thinking item.
	if e.Depth == 0 {
		s.Run.turnOutBytes += len(e.Delta)
	}
}

func (s *Session) applyToolStarted(e teasink.ToolStartedMsg) {
	s.logEvent("tool started", e.ToolName)
	s.Run.toolsRunning++
	if e.Depth > s.Run.maxDepth {
		s.Run.maxDepth = e.Depth
	}
	if e.ToolName == "load_skill" {
		if name, ok := e.Parameters["name"].(string); ok {
			if s.PendingSkillNames == nil {
				s.PendingSkillNames = make(map[string]string)
			}
			s.PendingSkillNames[e.ToolID] = name
		}
	}
}

func (s *Session) applyToolCompleted(e teasink.ToolCompletedMsg) toolCompletedEffect {
	s.LastToolResult = e.Result
	s.LastToolEffects = e.Effects
	s.logEvent("tool completed", e.ToolName)
	s.Run.foldTool(e.ToolName, e.Duration, false)

	if e.ToolName != "load_skill" {
		return toolCompletedEffect{}
	}
	name, ok := s.PendingSkillNames[e.ToolID]
	if ok {
		delete(s.PendingSkillNames, e.ToolID)
	}
	return toolCompletedEffect{LoadedSkillName: name}
}

func (s *Session) applyToolFailed(e teasink.ToolFailedMsg) {
	s.LastToolEffects = e.Effects
	s.logEvent("tool failed", e.ToolName+" ✗")
	s.Run.foldTool(e.ToolName, e.Duration, true)
	if e.ToolName == "load_skill" {
		delete(s.PendingSkillNames, e.ToolID)
	}
}

func (s *Session) applyDiff(e teasink.DiffMsg) {
	mutation := s.workingSet().RecordDiff(e.Path, e.Diff)
	s.logEvent("diff", e.Path)
	if e.Before != nil || e.After != nil || e.BeforeMissing || e.AfterMissing {
		s.checkpoints().RecordMutation(mutation, e.Before, e.BeforeMissing, e.After, e.AfterMissing)
	}
}

func (s *Session) applyPlanUpdated(e teasink.PlanUpdatedMsg) {
	done := 0
	for _, st := range e.Plan.Steps {
		if st.Status.String() == "completed" {
			done++
		}
	}
	s.logEvent("plan", fmt.Sprintf("%d/%d steps", done, len(e.Plan.Steps)))
	s.Plan = e.Plan
}

func (s *Session) applyIterationCompleted(e teasink.IterationCompletedMsg) {
	if e.Usage != nil {
		s.logEvent("iteration", fmt.Sprintf("#%d in=%d out=%d", e.Iter, e.Usage.PromptTokens, e.Usage.CompletionTokens))
	}
	if e.Depth == 0 {
		s.Run.foldIteration(e.Usage, e.Delta)
		s.Run.setContextBreakdown(e.Context)
	}
}

func (s *Session) applyCompactionApplied(e teasink.CompactionAppliedMsg) {
	s.logEvent("compaction", fmt.Sprintf("%d→%d msgs engine=%s", e.MessagesBefore, e.MessagesAfter, e.Engine))
	if e.Depth == 0 {
		s.Run.foldCompaction(e.MessagesBefore, e.MessagesAfter, e.BytesTrimmed, e.Engine)
	}
}

func (s *Session) applySteerInjected(e teasink.SteerInjectedMsg) {
	s.logEvent("steer", fmt.Sprintf("%d msgs", len(e.Messages)))
}

// applyConversationEnded folds the single terminal event. It switches on
// Reason: an error terminal surfaces an error toast and stops the run;
// any other reason (completed / max_iterations / cancelled) folds the
// turn's usage. The returned effect signals a toast change to the caller.
func (s *Session) applyConversationEnded(e teasink.ConversationEndedMsg, now time.Time) sessionEffect {
	s.LastParentToolCallID = e.ParentToolCallID
	s.logEvent("run ended", fmt.Sprintf("reason=%s iter=%d dur=%v", e.Reason, e.Iterations, e.Duration.Round(time.Millisecond)))
	if e.Depth == 0 {
		s.workingSet().CompleteTurn(e.TaskID)
		s.checkpoints().CompleteTurn(e.TaskID, now)
	}

	if e.Reason == runner.TerminalError {
		if e.Depth > 0 {
			return sessionEffect{}
		}
		msg := userFacingProviderError(e.Error)
		if e.RateLimit != nil {
			msg = formatRateLimit(e.RateLimit)
		}
		s.SetErrorToast(msg)
		s.Run.Running = false
		return sessionEffect{ToastChanged: true, Notice: palette.Error.On("✗ provider error: ") + msg}
	}

	if e.Depth == 0 {
		s.Run.foldTurnComplete(e.TotalUsage, e.Duration, e.Iterations)
	} else {
		s.Run.foldSubAgentUsage(e.TotalUsage)
	}
	return sessionEffect{}
}

var quotedProviderJSONRE = regexp.MustCompile(`(\{.*\})$`)

type providerErrorEnvelope struct {
	Error providerErrorBody `json:"error"`
}

type providerErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Param   string `json:"param"`
}

func userFacingProviderError(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "provider request failed"
	}
	if parsed, ok := parseProviderJSONError(msg); ok {
		return parsed
	}
	return msg
}

// formatRateLimit renders a rate-limit error for the user directly from its
// structured fields — no re-parsing of Error() text. Permanent quota
// exhaustion reads as a usage-limit notice; otherwise it appends a concrete
// "try again" hint and the provider's human message (never raw JSON).
func formatRateLimit(e *llm.RateLimitError) string {
	out := "rate limit"
	if e.Permanent {
		out = "usage limit reached"
	}
	if when := rateLimitWhen(e); when != "" {
		out += " — " + when
	}
	if detail := strings.TrimSpace(e.Message); detail != "" {
		out += ": " + detail
	}
	return out
}

// rateLimitWhen renders the "try again" hint. A clock time is only clear for
// a reset within the day; for longer windows a humanized duration ("resets
// in 2 days") reads better than a bare time-of-day.
func rateLimitWhen(e *llm.RateLimitError) string {
	switch {
	case !e.ResetAt.IsZero():
		if d := time.Until(e.ResetAt); d > 12*time.Hour {
			return "resets in " + humanizeDuration(d)
		}
		return "resets at " + e.ResetAt.Format(time.Kitchen)
	case e.RetryAfter > 0:
		return "retry in " + humanizeDuration(e.RetryAfter)
	default:
		return ""
	}
}

// humanizeDuration renders d in the largest sensible unit for a wait hint.
func humanizeDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours())/24)
	case d >= time.Hour:
		d = d.Round(time.Minute)
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}
}

func parseProviderJSONError(msg string) (string, bool) {
	raw := strings.TrimSpace(msg)
	if i := strings.Index(raw, "{"); i >= 0 {
		raw = raw[i:]
	}
	if unquoted, err := strconv.Unquote(raw); err == nil {
		raw = unquoted
	}
	var env providerErrorEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Error.Message == "" {
		if matches := quotedProviderJSONRE.FindStringSubmatch(msg); len(matches) == 2 {
			return parseProviderJSONError(matches[1])
		}
		return "", false
	}
	parts := []string{env.Error.Message}
	var meta []string
	if env.Error.Type != "" {
		meta = append(meta, env.Error.Type)
	}
	if env.Error.Code != "" {
		meta = append(meta, "code "+env.Error.Code)
	}
	if env.Error.Param != "" {
		meta = append(meta, "param "+env.Error.Param)
	}
	if len(meta) > 0 {
		parts = append(parts, "("+strings.Join(meta, "; ")+")")
	}
	return strings.Join(parts, " "), true
}
func (s *Session) consumeStartedPrompt(prompt string) string {
	if prompt == "" {
		return ""
	}
	if s.SkipStartedPrompt == "" {
		return prompt
	}
	skip := s.SkipStartedPrompt
	s.SkipStartedPrompt = ""
	if prompt == skip {
		return ""
	}
	return prompt
}

func trimPromptForNotice(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	return prompt
}
