package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zexec"
)

// RunHeadless executes one task to completion without a TUI, returning the
// runner's terminal result. It uses the same guarded tool set, tuned options,
// and compactor as the interactive path, with headless/eval policy overrides
// such as strict test-edit protection.
//
// By default headless is the harness's degenerate config: one attempt,
// trust the terminal reason. With a verify loop configured (SetVerifyLoop)
// it becomes a verified re-drive: the verify command is the world-checking
// oracle, failures feed back, and the agent continues with its transcript
// up to the attempt cap.
func (l *LiveRunner) RunHeadless(ctx context.Context, prompt string, maxIter int) runner.TaskResult {
	id := uuid.NewString()
	spec := runner.TaskSpec{
		ID:            taskscope.ID(id),
		Prompt:        prompt,
		MaxIterations: maxIter,
	}

	if !l.admission.enter() {
		return runner.TaskResult{ID: spec.ID, Reason: runner.TerminalError, Err: l.admission.rejection()}
	}
	defer l.admission.leave()

	// Preserve headless's immediate busy rejection while snapshotting context
	// only inside the cache transition gate.
	turnCtx, finish, err := l.beginTurn(ctx)
	if err != nil {
		return runner.TaskResult{ID: spec.ID, Reason: runner.TerminalError, Err: err}
	}
	defer finish()
	var result runner.TaskResult
	err = l.context.transition(turnCtx, spec, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		return func(seed runner.TaskSpec) runner.TaskResult {
			result = l.runHeadlessTurn(turnCtx, seed)
			return result
		}, nil
	}, finish)
	if err != nil {
		return runner.TaskResult{ID: spec.ID, Reason: runner.TerminalError, Err: err}
	}
	return result
}

func (l *LiveRunner) runHeadlessTurn(ctx context.Context, spec runner.TaskSpec) runner.TaskResult {
	// Persist the run lifecycle: a row at start, live progress after each
	// iteration (so a SIGKILL leaves a trail), and the terminal summary on
	// completion. The recorder is nil (no-op) when no store is configured.
	rec := l.newHeadlessRecorder(string(spec.ID))
	provider, model := l.headlessProviderModel()
	rec.start(ctx, spec.Prompt, provider, model)

	turn, err := l.buildTurnWithSource(ctx, l.headlessSource, runner.WithProgressUpdater(rec.progress))
	if err != nil {
		res := runner.TaskResult{ID: spec.ID, Reason: runner.TerminalError, Err: err}
		rec.complete(ctx, res)
		return res
	}

	// Optional early-stop: when an EarlyStopCommand is configured, stop the
	// attempt the moment that command passes (e.g. "keep editing until
	// `go test ./...` is green, then stop") instead of running to the
	// iteration cap. The watcher is a stop-hint; the bare Drive still trusts
	// the terminal reason as its verdict.
	supervisor := pursue.NewSupervisor()
	var reqOpts []pursue.RequestOption
	if w := l.earlyStopWatcher(supervisor); w != nil {
		reqOpts = append(reqOpts, pursue.WithWatcher(w))
	}

	// Verified re-drive: when a verify command is configured with attempts
	// to spend, it becomes the world-checking goal — each attempt's claim
	// of completion is checked by actually running the command; failures
	// feed back and the agent continues with the full transcript. This is
	// the eval-proven oracle+feedback+memory loop (8/10 verified vs the
	// 3/5 single-shot ceiling).
	var driveOpts []options.Option[pursue.Config]
	l.mu.Lock()
	verifyCmd, verifyAttempts := l.verifyCommand, l.verifyAttempts
	l.mu.Unlock()
	if verifyCmd != "" && verifyAttempts > 1 {
		root := l.ws.Root()
		goal := coderunner.CommandGoal(root, verifyCmd,
			func() string { return gitWorktreeState(root) },
			coderunner.VerifyOpts{OnResult: func(result coderunner.VerifyResult) {
				rec.verifierResult(ctx, result)
			}})
		reqOpts = append(reqOpts, pursue.WithGoal(goal))
		driveOpts = append(driveOpts, pursue.WithMaxAttempts(verifyAttempts),
			pursue.WithContextThreader(pursue.ThreadFullTranscript()),
		)
	}
	if rec != nil {
		driveOpts = append(driveOpts, pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			rec.attempt(ctx, report)
		}))
	}
	result := driveHeadlessOwned(ctx, supervisor, turn.runner, spec, reqOpts, driveOpts...)
	turn.close(ctx)
	rec.complete(ctx, result)
	return result
}

// earlyStopWatcher builds a Watcher from the configured EarlyStopCommand, or
// nil when none is set (the no-early-stop case). The probe runs the command
// in the workspace root, diff-gated and bounded so a periodic check stays
// cheap, and fires when it exits zero.
func (l *LiveRunner) earlyStopWatcher(supervisor *pursue.Supervisor) pursue.Watcher {
	l.mu.Lock()
	cmd := append([]string(nil), l.earlyStopCommand...)
	l.mu.Unlock()
	if len(cmd) == 0 {
		return nil
	}
	root := l.ws.Root()
	probe := coderunner.CommandProbe(root,
		func() string { return gitWorktreeState(root) },
		cmd,
		coderunner.ProbeOpts{PerRunTimeout: 2 * time.Minute, MaxRuns: 20, MinInterval: 10 * time.Second},
	)
	return supervisor.PollWatcher(probe, 3*time.Second)
}

// gitWorktreeState is the cheap diff snapshot CommandProbe gates on: the
// tracked diff plus the porcelain status (so new/deleted files register too).
// Best-effort — a git failure yields an empty string, which just means the
// gate can't suppress a run.
func gitWorktreeState(root string) string {
	// Bound both local git reads even though this callback receives no context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var b strings.Builder
	for _, args := range [][]string{
		{"-C", root, "diff", "--no-ext-diff", "--no-textconv", "--", "HEAD"},
		{"-C", root, "status", "--porcelain"},
	} {
		if out, err := gitOutput(ctx, args...); err == nil {
			b.Write(out)
		}
	}
	return b.String()
}

func gitOutput(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	zexec.StartProcessGroup(cmd)
	cmd.Cancel = func() error { return zexec.KillProcessGroup(cmd) }
	cmd.WaitDelay = time.Second
	defer func() { _ = zexec.KillProcessGroup(cmd) }()
	return cmd.Output()
}

func driveHeadlessOwned(ctx context.Context, supervisor *pursue.Supervisor, r *runner.Runner, spec runner.TaskSpec, reqOpts []pursue.RequestOption, driveOpts ...options.Option[pursue.Config]) runner.TaskResult {
	attempt := func(ctx context.Context, attemptSpec runner.TaskSpec) runner.TaskResult {
		var result runner.TaskResult
		func() {
			defer func() {
				if p := recover(); p != nil {
					slog.ErrorContext(ctx, "headless attempt panicked", "panic", p, "stack", string(debug.Stack()))
					result = runner.TaskResult{
						ID:     attemptSpec.ID,
						Reason: runner.TerminalError,
						Err:    fmt.Errorf("headless attempt panicked: %v", p),
					}
				}
			}()
			result = r.Run(ctx, attemptSpec)
		}()
		return result
	}

	var res runner.TaskResult
	var out pursue.Outcome
	drove := false
	// Inner func so the deferred recover can override res on panic without the
	// outer signature needing a named return.
	func() {
		defer func() {
			if p := recover(); p != nil {
				slog.ErrorContext(ctx, "headless run panicked", "panic", p, "stack", string(debug.Stack()))
				res = runner.TaskResult{
					ID:     spec.ID,
					Reason: runner.TerminalError,
					Err:    fmt.Errorf("headless run panicked: %v", p),
				}
			}
		}()
		out = supervisor.Drive(ctx, pursue.NewRequest(attempt, spec, reqOpts...), driveOpts...)
		drove = true
	}()
	_ = supervisor.Join(context.WithoutCancel(ctx))
	settled, completed := supervisor.LastResult()
	if !drove {
		return res
	}

	res = out.Result
	if err := out.Err(); err != nil {
		if completed {
			res = settled
		}
		res.ID = spec.ID
		res.Reason = runner.TerminalError
		res.Err = err
	}
	if out.Attempts > 1 || out.Verified {
		slog.InfoContext(ctx, "headless verified re-drive",
			"attempts", out.Attempts, "verified", out.Verified, "reason", res.Reason)
	}
	return res
}

// headlessProviderModel snapshots the active provider name + model for the
// run row.
// Returns (provider name, model).
func (l *LiveRunner) headlessProviderModel() (string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target.Spec.Name, l.target.Model
}

// HeadlessReport is the stable cold-start measurement emitted for one headless
// run. It combines the exact request surface with the rendered system prompt and
// provider usage, so local and cloud runs share one JSON shape.
type HeadlessReport struct {
	PromptBytes      int                   `json:"prompt_bytes"`
	PromptWords      int                   `json:"prompt_words"`
	ToolCount        int                   `json:"tool_count"`
	ToolJSONBytes    int                   `json:"tool_json_bytes"`
	ToolFingerprint  string                `json:"tool_fingerprint"`
	PromptTokens     int                   `json:"prompt_tokens,omitempty"`
	CachedTokens     int                   `json:"cached_tokens,omitempty"`
	CompletionTokens int                   `json:"completion_tokens,omitempty"`
	TerminalReason   runner.TerminalReason `json:"terminal_reason"`
	TerminalCause    runner.TerminalCause  `json:"terminal_cause,omitempty"`
	Iterations       int                   `json:"iterations"`
	Duration         time.Duration         `json:"duration"`
	// TotalUsage sums reported attempt snapshots for this Run only; nil means
	// unreported. Timing.AttemptsWithUsage identifies partial totals. Descendant
	// usage and earlier verified re-drive Runs are not included.
	TotalUsage *HeadlessUsage    `json:"total_usage"`
	Timing     runner.TaskTiming `json:"timing"`
	// UnattributedDuration retains setup, compaction, backoff and settlement
	// rather than presenting those as provider or tool execution time.
	UnattributedDuration time.Duration `json:"unattributed_duration"`
}

// HeadlessUsage is the reported whole-Run token total, distinct from latest
// context occupancy. CachedTokens retains provider semantics: providers that
// omit cache accounting cannot be distinguished from reported zero here.
type HeadlessUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Report returns cold-start accounting for res.
func Report(res runner.TaskResult) HeadlessReport {
	report := HeadlessReport{
		PromptBytes:          len(res.SystemPrompt),
		PromptWords:          len(strings.Fields(res.SystemPrompt)),
		ToolCount:            res.ToolSurface.Count,
		ToolJSONBytes:        res.ToolSurface.JSONBytes,
		ToolFingerprint:      res.ToolSurface.Fingerprint,
		TerminalReason:       res.Reason,
		TerminalCause:        res.Cause,
		Iterations:           res.Iterations,
		Duration:             res.Duration,
		Timing:               res.Timing,
		UnattributedDuration: res.Duration - res.Timing.ProviderDuration - res.Timing.RequestPreparationDuration - res.Timing.ToolDispatchDuration,
	}
	if res.LastUsage != nil {
		report.PromptTokens = res.LastUsage.PromptTokens
		report.CachedTokens = res.LastUsage.CachedTokens
		report.CompletionTokens = res.LastUsage.CompletionTokens
	}
	if u := res.TotalUsage; u != nil {
		report.TotalUsage = &HeadlessUsage{PromptTokens: u.PromptTokens, CachedTokens: u.CachedTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens}
	}
	return report
}

// RunHeadlessProcess drives one headless task and maps the terminal reason to a
// process exit code: 0 completed, 1 bounded/cancelled, 2 error, 4 bad input.
// The run summary goes to stderr so stdout remains the final answer. reportOut,
// when non-nil, receives one JSON cold-start report.

func RunHeadlessProcess(ctx context.Context, live *LiveRunner, prompt string, maxIter int, reportOut *os.File) int {
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "headless: --prompt-file or --prompt-text required")
		return 4
	}
	fmt.Fprintf(os.Stderr, "headless: workspace=%s\n", live.ws.Root())

	res := live.RunHeadless(ctx, prompt, maxIter)
	fmt.Fprintf(os.Stderr,
		"headless: terminated reason=%s iterations=%d duration=%s tool_calls=%d\n",
		res.Reason, res.Iterations, res.Duration, coderunner.ToolCallCount(res.Messages))
	if reportOut != nil {
		if err := json.NewEncoder(reportOut).Encode(Report(res)); err != nil {
			fmt.Fprintln(os.Stderr, "headless: report:", err)
			return 2
		}
	}

	switch res.Reason {
	case runner.TerminalCompleted:
		if res.FinalContent != "" {
			fmt.Fprintln(os.Stdout, res.FinalContent)
		}
		return 0
	case runner.TerminalError:
		if res.Err != nil {
			fmt.Fprintln(os.Stderr, "headless: error:", res.Err)
		}
		return 2
	default:
		return 1
	}
}
