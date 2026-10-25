package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// PerformanceDiagnostics describes the current or last root task, excluding
// descendants. Durations are client-observed wall times, not server inference.
// Iteration phase sums include completed iterations only; failed preparation,
// compaction, backoff and settlement can remain unobserved. Neighboring cache,
// mode and tool observations do not establish causes for latency or cache misses.
type PerformanceDiagnostics struct {
	TaskID            string
	Provider          string
	Model             string
	CurrentMode       string
	Attempts          int
	AttemptsWithUsage int
	ProviderDuration  time.Duration
	// CallbackDuration is included in ProviderDuration, not an additional phase.
	CallbackDuration    time.Duration
	LastAttempt         int
	LastAttemptDuration time.Duration
	// TimeToFirstOutput is relative to the latest attempt. Nil means no output;
	// a non-nil zero means immediate content, thinking or tool-call output.
	TimeToFirstOutput          *time.Duration
	LastAttemptUsageReported   bool
	LastAttemptUsage           llm.Usage
	CompletedIterations        int
	LastIteration              int
	RequestPreparationDuration time.Duration
	ToolDispatchDuration       time.Duration
	// ToolSurface belongs to LastIteration, independently of LastAttempt.
	ToolSurface              runner.ToolSurface
	Compactions              int
	Settled                  bool
	Duration                 time.Duration
	ReportedCompletionTokens int
	// ReportedTokensPerSecond includes tools and waiting. Missing provider usage
	// can make this partial; it is never a server decoding-speed measurement.
	ReportedTokensPerSecond float64
	UsageReported           bool
}

// PerformanceDiagnostics returns an owned transient snapshot for the current or
// last root task. Like other UI snapshots, call it from the UI event owner.
func (m *UI) PerformanceDiagnostics() PerformanceDiagnostics {
	p := m.session.Run.performance
	if p.TimeToFirstOutput != nil {
		p.TimeToFirstOutput = new(*p.TimeToFirstOutput)
	}
	p.CurrentMode = "BUILD"
	if m.session.PlanMode {
		p.CurrentMode = "PLAN"
	}
	if !p.Settled && p.TaskID != "" {
		p.Duration = time.Since(m.session.Run.turnStartedAt)
		p.UsageReported = p.AttemptsWithUsage > 0
	}
	if p.UsageReported && p.Duration > 0 {
		p.ReportedTokensPerSecond = float64(p.ReportedCompletionTokens) / p.Duration.Seconds()
	}
	return p
}

func (s *RunState) observeAttemptTiming(e teasink.ProviderAttemptSettledMsg) {
	p := &s.performance
	p.Attempts++
	p.ProviderDuration += e.Duration
	p.CallbackDuration += e.CallbackDuration
	p.LastAttempt = e.Attempt
	p.LastAttemptDuration = e.Duration
	p.TimeToFirstOutput = nil
	if e.TimeToFirstOutput != nil {
		p.TimeToFirstOutput = new(*e.TimeToFirstOutput)
	}
	p.LastAttemptUsageReported = e.Usage != nil
	p.LastAttemptUsage = llm.Usage{}
	if e.Usage != nil {
		p.AttemptsWithUsage++
		p.ReportedCompletionTokens += e.Usage.CompletionTokens
		p.LastAttemptUsage = *e.Usage
	}
}

func (s *Session) observeIterationTiming(e teasink.IterationCompletedMsg) {
	task, ok := s.Run.tasks[e.TaskID]
	if !ok || task.depth != e.Depth || e.Depth != 0 || (task.iterationSeen && e.Iter <= task.iteration) {
		return
	}
	task.iterationSeen, task.iteration = true, e.Iter
	s.Run.tasks[e.TaskID] = task
	p := &s.Run.performance
	p.CompletedIterations++
	p.LastIteration = e.Iter
	p.RequestPreparationDuration += e.RequestPreparationDuration
	p.ToolDispatchDuration += e.ToolDispatchDuration
	p.ToolSurface = e.ToolSurface
}

func (m *UI) performanceLines(width int) []string {
	p := m.PerformanceDiagnostics()
	if p.TaskID == "" {
		return nil
	}
	first := "none"
	if p.TimeToFirstOutput != nil {
		first = p.TimeToFirstOutput.Round(time.Millisecond).String()
	}
	lines := []string{
		fmt.Sprintf("root only · current mode %s", p.CurrentMode),
		fmt.Sprintf("%d attempts · provider %s", p.Attempts, p.ProviderDuration.Round(time.Millisecond)),
		fmt.Sprintf("callbacks %s (inside provider)", p.CallbackDuration.Round(time.Millisecond)),
		fmt.Sprintf("attempt %d · first output %s", p.LastAttempt, first),
		fmt.Sprintf("completed iterations %d (partial phases)", p.CompletedIterations),
		fmt.Sprintf("prep %s · tools %s", p.RequestPreparationDuration.Round(time.Millisecond), p.ToolDispatchDuration.Round(time.Millisecond)),
	}
	if p.LastAttemptUsageReported {
		lines = append(lines, fmt.Sprintf("attempt cache read %d · create %d", p.LastAttemptUsage.CachedTokens, p.LastAttemptUsage.CacheCreationTokens))
	} else {
		lines = append(lines, "attempt usage/cache unreported")
	}
	if p.CompletedIterations > 0 {
		lines = append(lines, fmt.Sprintf("iter %d tools %d · %d B · changed %t", p.LastIteration, p.ToolSurface.Count, p.ToolSurface.JSONBytes, p.ToolSurface.Changed))
	}
	if p.UsageReported {
		lines = append(lines, fmt.Sprintf("%.1f reported tok/s end-to-end", p.ReportedTokensPerSecond))
	}
	lines = append(lines, fmt.Sprintf("compactions %d · observations, not causes", p.Compactions), "not server decode speed; usage may be partial")
	for i, line := range lines {
		lines[i] = ansi.Truncate(palette.Muted.On(line), width, "…")
	}
	return lines
}
