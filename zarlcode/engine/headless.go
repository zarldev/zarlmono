package engine

import (
	"context"
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
)

// RunHeadless executes one task to completion without a TUI, returning the
// runner's terminal result. It reuses [LiveRunner.buildTurn] — the same
// guarded tool set, tuned options, and compactor the interactive path builds,
// with headless/eval policy overrides such as strict test-edit protection.
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

	// Persist the run lifecycle: a row at start, live progress after each
	// iteration (so a SIGKILL leaves a trail), and the terminal summary on
	// completion. The recorder is nil (no-op) when no store is configured.
	rec := l.newHeadlessRecorder(id)
	provider, model := l.headlessProviderModel()
	rec.start(ctx, prompt, provider, model)

	r, err := l.buildHeadlessTurn(runner.WithProgressUpdater(rec.progress))
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
	var reqOpts []pursue.RequestOption
	if w := l.earlyStopWatcher(); w != nil {
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
			coderunner.VerifyOpts{})
		reqOpts = append(reqOpts, pursue.WithGoal(goal))
		driveOpts = append(driveOpts,
			pursue.WithMaxAttempts(verifyAttempts),
			pursue.WithContextThreader(pursue.ThreadFullTranscript()),
		)
	}
	return driveHeadless(ctx, r, spec, rec, reqOpts, driveOpts...)
}

// earlyStopWatcher builds a Watcher from the configured EarlyStopCommand, or
// nil when none is set (the no-early-stop case). The probe runs the command
// in the workspace root, diff-gated and bounded so a periodic check stays
// cheap, and fires when it exits zero.
func (l *LiveRunner) earlyStopWatcher() pursue.Watcher {
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
	return pursue.PollWatcher(probe, 3*time.Second)
}

// gitWorktreeState is the cheap diff snapshot CommandProbe gates on: the
// tracked diff plus the porcelain status (so new/deleted files register too).
// Best-effort — a git failure yields an empty string, which just means the
// gate can't suppress a run.
func gitWorktreeState(root string) string {
	var b strings.Builder
	for _, args := range [][]string{
		{"-C", root, "diff"},
		{"-C", root, "status", "--porcelain"},
	} {
		// No ctx reaches this probe callback; these are quick local git reads
		// and the surrounding CommandProbe enforces its own per-run timeout.
		if out, err := exec.CommandContext(context.Background(), "git", args...).Output(); err == nil {
			b.Write(out)
		}
	}
	return b.String()
}

// driveHeadless runs the single headless attempt and records its terminal
// state. A panic in the runner is recovered and recorded as a terminal
// error so the row never strands "in flight" and the process exits
// cleanly (mapped to exit code 2 by RunHeadlessProcess).
func driveHeadless(ctx context.Context, r *runner.Runner, spec runner.TaskSpec, rec *headlessRecorder, reqOpts []pursue.RequestOption, driveOpts ...options.Option[pursue.Config]) runner.TaskResult {
	var res runner.TaskResult
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
				rec.complete(ctx, res)
			}
		}()
		out := pursue.Drive(ctx, pursue.NewRequest(r.Run, spec, reqOpts...), driveOpts...)
		res = out.Result
		if out.Attempts > 1 || out.Verified {
			slog.InfoContext(ctx, "headless verified re-drive",
				"attempts", out.Attempts, "verified", out.Verified, "reason", res.Reason)
		}
		rec.complete(ctx, res)
	}()
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

// RunHeadlessProcess drives one --headless task and maps the terminal reason to a
// process exit code:
//
//	0  TerminalCompleted (agent settled on a final answer)
//	1  TerminalMaxIterations or TerminalCancelled (ran out of room)
//	2  TerminalError (provider blew up / tool dispatch panicked)
//	4  Bad invocation (missing prompt)
//
// The run summary goes to stderr so the eval harness can read it without
// parsing stdout, which carries only the agent's final answer.
func RunHeadlessProcess(ctx context.Context, live *LiveRunner, prompt string, maxIter int) int {
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "headless: --prompt-file or --prompt-text required")
		return 4
	}
	fmt.Fprintf(os.Stderr, "headless: workspace=%s\n", live.ws.Root())

	res := live.RunHeadless(ctx, prompt, maxIter)
	fmt.Fprintf(os.Stderr,
		"headless: terminated reason=%s iterations=%d duration=%s tool_calls=%d\n",
		res.Reason, res.Iterations, res.Duration, coderunner.ToolCallCount(res.Messages))

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
