package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/zarldev/zarlmono/swebench-eval/internal/evaluator"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Driver defaults mirroring zarlcode's TUI so an eval run and an
// interactive session behave identically when their per-run knobs match.
const (
	defaultContextWindow = 32768
	defaultReserveTokens = 512
	// defaultVerifyTimeout bounds one per-attempt SWE-bench evaluator run
	// (Docker image build + test suite). Generous because a cold image build
	// dominates the first run; independent of the agent's per-task Timeout.
	defaultVerifyTimeout = 30 * time.Minute
	// cleanupTimeout bounds the post-run process-manager close and the
	// worktree diff capture. Both ran on context.Background() before, so a
	// wedged git diff (corrupt repo, huge binary blob) or a process that
	// ignores its kill could stall result collection forever.
	cleanupTimeout = 30 * time.Second
	// maxVerifyDiffBytes caps the model-generated patch written into a
	// prediction file before shelling to the SWE-bench evaluator. A
	// pathological multi-megabyte diff (runaway generation, embedded blob)
	// would otherwise exhaust disk/memory on serialization. Real SWE-bench
	// patches are a few KB; 5 MiB is a generous ceiling.
	maxVerifyDiffBytes = 5 << 20
)

// ZarlcodeDriver drives the zarlcode coding agent IN-PROCESS against a
// SWE-bench worktree. It builds the same loop the zarlcode TUI runs —
// the standard tool set, the production guardrail chain, the tuned
// runner options, all from zkit/agent/coderunner — and drives it through
// zkit/agent/pursue. There is no subprocess and no state.db round-trip:
// the diff and telemetry come straight off the runner's TaskResult, so
// "what I tested in the TUI" and "what eval submitted" share one code
// path and cannot drift.
//
// The provider is built once (via zarlcode's vault + provider registry)
// and shared across tasks; providers are safe for concurrent Complete
// calls, so one client serves every parallel worker.
type ZarlcodeDriver struct {
	// Provider pins the backend by registry name (e.g. "llamacpp",
	// "openai-codex", "gemini", "claude-code"). Empty defaults to the
	// registry default (llamacpp).
	Provider string
	// Model pins the model id. Empty uses the provider definition's
	// default model.
	Model string
	// CodexEffort sets codex_reasoning_effort when Provider is
	// "openai-codex" (low/medium/high/xhigh). Empty leaves the
	// per-model heuristic in place.
	CodexEffort string
	// StateDB is zarlcode's sqlite path (vault + custom-provider rows).
	// Empty resolves to ~/.zarlcode/state.db.
	StateDB string
	// EnvFile is an optional .env loaded once before provider
	// construction so per-backend URL knobs (LLAMACPP_BASE_URL,
	// OPENAI_BASE_URL, GEMINI_API_KEY, …) are in the environment the
	// registry resolves against.
	EnvFile string
	// MaxIter caps the agent loop. Zero uses the loop's default.
	MaxIter int
	// ToolConcurrency caps concurrent tool dispatch per batch. Zero is
	// sequential.
	ToolConcurrency int
	// ContextWindow sizes the compactor. Zero uses defaultContextWindow.
	ContextWindow int
	// LlamacppResetURL, when set, is POSTed before each task to flush
	// the local llama-server's KV cache slot so tasks don't inherit each
	// other's state. Canonical value:
	// http://localhost:8081/slots/0?action=erase (requires
	// --slot-save-path on the server). Empty disables.
	LlamacppResetURL string
	// AllowRemoteResetURL lifts the loopback restriction on
	// LlamacppResetURL. Default false: the reset endpoint must resolve to
	// localhost so a misconfigured (or attacker-supplied) URL can't turn
	// the per-task reset into an SSRF POST against an arbitrary host.
	AllowRemoteResetURL bool

	// VerifiedAttempts enables harness-level re-drive with the official
	// SWE-bench evaluator as the Goal oracle. Values <= 1 keep the historical
	// headless shape (one attempt, trust TerminalCompleted). Values > 1 run the
	// current patch through the evaluator after each attempt; unresolved patches
	// are fed back to the agent until the cap is reached.
	VerifiedAttempts int
	// VerifyDataset/Python/Workers/WorkDir tune the per-attempt SWE-bench verifier
	// used when VerifiedAttempts > 1. Empty values use the same defaults as the
	// post-run scorer.
	VerifyDataset string
	VerifyPython  string
	VerifyWorkers int
	VerifyWorkDir string
	// VerifyTimeout bounds a single per-attempt verifier invocation (Docker
	// image build + test run). It is independent of the agent's per-task
	// Timeout so a slow evaluator can't be starved by the agent's remaining
	// clock and read as a verifier failure. Zero uses defaultVerifyTimeout.
	VerifyTimeout time.Duration
	// ThreadTranscript selects pursue's full-transcript threading for
	// verified re-drives: each retry carries the prior attempt's entire
	// message history, so the agent continues where it left off instead of
	// starting from a restated prompt over a dirty worktree. Affordable
	// only with a real context window — the historic feedback-only
	// threading (false) was a 32k-era prompt-budget decision.
	ThreadTranscript bool

	// TranscriptDir, when set, persists each task's full agent message
	// history to <dir>/<instance_id>.json after the run so empty/failed
	// patches stay diagnosable once the worktree is cleaned up. Empty disables.
	TranscriptDir string

	// Ablation selects the guardrail-chain variant this driver instance
	// runs (zero value = the full production baseline). Each arm reports
	// as its own driver name, so one eval run can carry several arms and
	// the existing per-driver comparison machinery does the grouping.
	Ablation Ablation

	// once guards lazy provider construction; the provider + its backing
	// state.db/vault outlive individual tasks.
	once     sync.Once
	env      *providerEnv
	prov     llm.Provider
	buildErr error
}

// Name identifies the driver in reports; ablation arms suffix it so each
// arm keys its own eval_results rows. Hyphen separator, NOT a colon: the
// name flows into the SWE-bench evaluator's Docker container names, which
// only allow [a-zA-Z0-9][a-zA-Z0-9_.-] — a colon made container creation
// fail for every arm instance, so arm patches were never evaluated.
func (d *ZarlcodeDriver) Name() string {
	if d.Ablation.Name == "" || d.Ablation.Name == "baseline" {
		return "zarlcode"
	}
	return "zarlcode-" + d.Ablation.Name
}

// ensureProvider builds the provider once. Uses a background context so
// the provider (and its token source) outlives any single task's ctx.
func (d *ZarlcodeDriver) ensureProvider() error {
	d.once.Do(func() {
		ctx := context.Background()
		build := func() {
			env, err := openProviderEnv(ctx, d.StateDB)
			if err != nil {
				d.buildErr = err
				return
			}
			prov, err := env.buildProvider(ctx, d.Provider, d.Model, d.CodexEffort)
			if err != nil {
				env.close()
				d.buildErr = fmt.Errorf("build provider %q: %w", d.Provider, err)
				return
			}
			d.env = env
			d.prov = prov
		}
		if d.EnvFile == "" {
			build()
			return
		}
		// The provider captures its key/base-URL (and the vault its master
		// key) synchronously during build, so the .env only needs to be live
		// for that window. Apply it transiently and restore afterwards rather
		// than godotenv.Overload's permanent os.Environ mutation: that leaked
		// one driver's .env into another's provider resolution under
		// concurrent builds and outlived the run.
		if err := withEnvFile(d.EnvFile, build); err != nil {
			d.buildErr = err
		}
	})
	return d.buildErr
}

// envApplyMu serializes the transient os.Environ mutation in withEnvFile so
// two drivers building concurrently can't interleave each other's .env into
// the global environment.
var envApplyMu sync.Mutex

// withEnvFile loads path's key/value pairs, installs them over the current
// process environment (the .env wins, matching zarlcode's --env), runs fn,
// then restores every touched variable to its prior state. A read error is
// returned rather than swallowed so a bad --env path fails loudly.
func withEnvFile(path string, fn func()) error {
	vals, err := godotenv.Read(path)
	if err != nil {
		return fmt.Errorf("read env file %q: %w", path, err)
	}
	envApplyMu.Lock()
	defer envApplyMu.Unlock()
	type prior struct {
		val string
		set bool
	}
	saved := make(map[string]prior, len(vals))
	for k, v := range vals {
		old, ok := os.LookupEnv(k)
		saved[k] = prior{old, ok}
		_ = os.Setenv(k, v)
	}
	defer func() {
		for k, p := range saved {
			if p.set {
				_ = os.Setenv(k, p.val)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}()
	fn()
	return nil
}

// Close releases the driver's state.db handle. Call once after the eval
// run finishes (the provider is shared across every task).
func (d *ZarlcodeDriver) Close() {
	if d.env != nil {
		d.env.close()
	}
}

// Run executes one task in-process: build the workspace + tool registry,
// arm the guardrail chain, construct the runner with the shared tuned
// options, drive it through the harness, and capture the worktree diff.
//
// Returns Result.Err non-nil only for driver-level failures (provider
// build, workspace open). The agent reporting "I couldn't solve this"
// surfaces as a successful Run with a terminal reason and a diff the
// SWE-bench evaluator subsequently scores.
func (d *ZarlcodeDriver) Run(ctx context.Context, t Task) Result {
	start := time.Now()

	if err := d.ensureProvider(); err != nil {
		return Result{Err: err, Duration: time.Since(start)}
	}

	// Evict the llama-server KV slot before this task so it doesn't
	// inherit accumulated context from a prior one. Non-fatal.
	if d.LlamacppResetURL != "" {
		if err := resetLlamacppSlot(ctx, d.LlamacppResetURL, d.AllowRemoteResetURL); err != nil {
			fmt.Fprintf(os.Stderr, "zarlcode driver: llamacpp reset %q failed: %v\n", d.LlamacppResetURL, err)
		}
	}

	ws, err := code.NewWorkspace(t.RepoPath)
	if err != nil {
		return Result{Err: fmt.Errorf("workspace %q: %w", t.RepoPath, err), Duration: time.Since(start)}
	}
	root := ws.Root()

	// Kernel sandbox for the agent's shell: same Landlock policy the TUI
	// uses, rooted at the task worktree (DefaultPolicy grants the linked
	// .git common dir, so git inside the worktree keeps working). On a
	// kernel without Landlock the run proceeds unconfined with a notice —
	// an eval shouldn't silently change shape, just say so.
	var sb code.Sandboxer
	if !sandbox.EnabledFromEnv() {
		fmt.Fprintln(os.Stderr, "zarlcode driver: sandbox disabled via ZARLCODE_SANDBOX")
	} else if s, serr := sandbox.New(sandbox.DefaultPolicy(root)); serr != nil {
		fmt.Fprintf(os.Stderr, "zarlcode driver: sandbox unavailable, running unconfined: %v\n", serr)
	} else {
		sb = s
	}

	pm := code.NewProcessManager(ws,
		code.WithMaxAliveProcesses(16),
		code.WithProcessOutputBuffer(10000),
		code.WithProcessSandbox(sb),
	)
	defer func() {
		// Bound cleanup so a process that ignores its kill can't wedge the
		// whole run; context.Background() here had no deadline.
		cctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		pm.Close(cctx)
	}()

	reg := tools.NewRegistry()
	coderunner.RegisterStandardTools(reg, ws, pm, coderunner.WithToolSandbox(sb))

	// Strict test-edit: the grader's FAIL_TO_PASS tests must survive the agent
	// untouched, so block edits to test files outright. GoVerifier + fan-out
	// caps come from the shared coderunner invariant. The ablation arm then
	// drops its guardrail(s) and/or arms the constrained-verdict judge on the
	// run's own provider.
	deps := coderunner.StandardGuardrailDeps(root, guardrails.NewTestEditStrict())
	deps.Disabled = d.Ablation.Disabled
	if d.Ablation.Judge {
		deps.DecomposeJudge = guardrails.NewLLMVerdictJudge(d.prov)
	}
	source, _, err := coderunner.GuardedSource(reg, deps, sourcechain.Pipeline{})
	if err != nil {
		return Result{Err: fmt.Errorf("arm guardrails: %w", err), Duration: time.Since(start)}
	}

	prompt, err := renderSystemPrompt(root, reg)
	if err != nil {
		return Result{Err: fmt.Errorf("render system prompt: %w", err), Duration: time.Since(start)}
	}

	ctxWindow := d.ContextWindow
	if ctxWindow <= 0 {
		ctxWindow = defaultContextWindow
	}
	// Pressure-gated tiered compactor — identical to the TUI's: the
	// runner consults it each iteration but it only fires Compact when
	// observed usage is within ReserveTokens of the window.
	compactor := coderunner.StandardCompactor(compact.NewTiered(ctxWindow), ctxWindow, defaultReserveTokens)

	// Tail-cap oversized tool results and spill the full text to disk (under
	// TempDir, NOT the worktree — a spill in the tree would pollute the
	// captured diff). One per task; Cleanup removes the spill dir on return.
	truncator := &runner.SpillingTruncator{Prefix: "swebench-"}
	defer func() { _ = truncator.Cleanup() }()

	opts := coderunner.StandardOptions(coderunner.Tuning{
		Model:           d.Model,
		MaxIterations:   d.MaxIter,
		ToolConcurrency: d.ToolConcurrency,
		ContextWindow:   ctxWindow,
	})
	// Iteration + stream-idle watchdogs now live in StandardOptions (shared
	// with the TUI so the two can't drift); eval no longer sets its own.
	opts = append(opts,
		runner.WithPrompt(runner.StaticPrompt(prompt)),
		runner.WithCompactor(compactor),
		runner.WithResultTruncator(truncator),
		// Completion gate: every SWE-bench task is graded on a non-empty
		// diff, so a run that ends having edited nothing is a guaranteed
		// miss. Catch it in-loop — inject a "you haven't changed anything"
		// nudge and keep going — instead of letting the empty attempt
		// complete and leaning on a re-drive to clean up. Bounded so a
		// model that truly can't find the change still terminates. Eval-
		// only: the interactive TUI legitimately answers without editing.
		runner.WithCompletionGate(runner.RequireWork{MaxCorrections: 2}),
	)
	opts = append(opts, runner.WithTools(source))
	r := runner.New(runner.ClientFromProvider(d.prov), opts...)

	// Snapshot pre-run untracked files so the captured diff attributes
	// only the agent's new files (SWE-bench worktrees are clean, so this
	// is usually empty — but cheap insurance).
	preexisting := code.UntrackedFiles(ctx, root)

	runCtx := ctx
	if t.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}
	if d.VerifiedAttempts > 1 {
		if err := evaluator.EnsureAvailable(runCtx, d.verifyPython()); err != nil {
			return Result{Err: fmt.Errorf("verified evaluator: %w", err), Duration: time.Since(start)}
		}
	}

	spec := runner.TaskSpec{
		ID:            taskscope.ID(uuid.NewString()),
		Prompt:        taskPrompt(t),
		MaxIterations: d.MaxIter,
	}

	req := pursue.NewRequest(r.Run, spec)
	var driveOpts []options.Option[pursue.Config]
	var verdicts *attemptVerdictLog
	if d.VerifiedAttempts > 1 {
		verdicts = &attemptVerdictLog{}
		// Pass the eval-lifetime ctx (not runCtx): verification must not be
		// bounded by the agent's per-task Timeout.
		req = pursue.NewRequest(r.Run, spec, pursue.WithGoal(d.failToPassGoal(ctx, root, preexisting, t, verdicts)))
		threader := swebenchContextThreader
		if d.ThreadTranscript {
			threader = pursue.ThreadFullTranscript()
		}
		driveOpts = append(driveOpts,
			pursue.WithMaxAttempts(d.VerifiedAttempts),
			pursue.WithContextThreader(threader),
		)
	}
	out := pursue.Drive(runCtx, req, driveOpts...)
	res := out.Result
	duration := time.Since(start)

	if d.TranscriptDir != "" {
		writeTranscript(d.TranscriptDir, t.ID, res.Messages)
	}

	// Bound the final diff capture: a corrupt repo or a huge binary diff
	// must not stall result collection (was context.Background()).
	diffCtx, diffCancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer diffCancel()
	result := Result{
		Diff:                code.WorktreeDiff(diffCtx, root, t.BaseCommit, preexisting),
		Duration:            duration,
		ToolCalls:           coderunner.ToolCallCount(res.Messages),
		GuardrailRejections: coderunner.GuardrailRejectionCounts(res.Messages),
		Iterations:          res.Iterations,
		TerminalReason:      terminalReasonString(res.Reason),
		Verified:            out.Verified,
		Attempts:            out.Attempts,
		AttemptVerdicts:     verdicts.snapshot(),
		Provider:            d.reportProvider(),
		Model:               d.Model,
	}
	// TotalUsage sums every iteration; LastUsage under-reports multi-turn
	// runs. Zero stays the "unknown" sentinel when the run made no LLM call.
	if u := res.TotalUsage; u != nil {
		result.TokensIn = int64(u.PromptTokens)
		result.TokensOut = int64(u.CompletionTokens)
	}
	if res.Reason == runner.TerminalError && res.Err != nil {
		result.Err = res.Err
	}
	return result
}

// terminalReasonString maps the runner's terminal reason to the eval's
// stable wire string — the value persisted in the results DB and grouped on
// by the report. Switching over the typed constants makes a runner-side
// rename a compile error here rather than a silent regrouping; returning
// eval-owned literals keeps the DB contract stable even if the runner ever
// changes its own underlying string values.
func terminalReasonString(r runner.TerminalReason) string {
	switch r {
	case runner.TerminalCompleted:
		return "completed"
	case runner.TerminalMaxIterations:
		return "max_iterations"
	case runner.TerminalError:
		return "error"
	case runner.TerminalCancelled:
		return "cancelled"
	default:
		return string(r)
	}
}

// attemptVerdictLog accumulates the per-attempt goal verdicts for the
// result row. Attempts are sequential, but the goal may be evaluated on a
// different goroutine than the driver reads from — a small mutex keeps it
// honest either way. A nil log is a no-op recorder.
type attemptVerdictLog struct {
	mu  sync.Mutex
	all []AttemptVerdict
}

func (l *attemptVerdictLog) record(v AttemptVerdict) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.all = append(l.all, v)
	l.mu.Unlock()
}

func (l *attemptVerdictLog) snapshot() []AttemptVerdict {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AttemptVerdict(nil), l.all...)
}

func (d *ZarlcodeDriver) failToPassGoal(parentCtx context.Context, root string, preexisting map[string]bool, task Task, verdicts *attemptVerdictLog) pursue.Goal {
	// The attempt context Drive passes to Evaluate is the agent's per-task
	// (timeout-bounded) context. Verification runs on parentCtx — the
	// eval-lifetime context — so a slow Docker evaluator isn't charged against
	// the agent's remaining clock; verifyPatch bounds it with VerifyTimeout.
	// Verified: the verdict comes from running the official SWE-bench
	// FAIL_TO_PASS verifier against the worktree diff — real world
	// verification, not a trusted terminal reason — so a resolved Outcome
	// reports Verified == true.
	return pursue.Verified(pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		// Diff against the task's pinned base commit, not "" (HEAD): if the
		// agent commits its work, HEAD moves to that commit and a HEAD-relative
		// diff goes empty, falsely flagging a correct patch as empty. The base
		// commit is stable across any commits the agent makes mid-attempt.
		diff := code.WorktreeDiff(parentCtx, root, task.BaseCommit, preexisting)
		if strings.TrimSpace(diff) == "" {
			// Diagnose the empty: head_moved=true means the agent committed but
			// produced no net change vs base (genuine empty, not a lost diff —
			// the base-commit diff above would have caught a real committed
			// patch); head_moved=false with low tool_calls means it never edited.
			headMoved := code.GitHead(parentCtx, root) != task.BaseCommit
			fmt.Fprintf(os.Stderr, "zarlcode driver: empty patch instance=%s attempt=%d tool_calls=%d head_moved=%t\n",
				task.ID, attempt.Number, coderunner.ToolCallCount(attempt.Result.Messages), headMoved)
			verdicts.record(AttemptVerdict{Attempt: attempt.Number, EmptyPatch: true})
			return pursue.Retry(verifiedFeedback(task, attempt, "the patch is empty; no fail-to-pass tests can pass yet"))
		}
		verdict, err := d.verifyPatch(parentCtx, task.ID, attempt.Number, diff)
		if err != nil {
			verdicts.record(AttemptVerdict{Attempt: attempt.Number, Error: err.Error()})
			return pursue.Retry(verifiedFeedback(task, attempt, fmt.Sprintf("the SWE-bench verifier could not run: %v", err)))
		}
		verdicts.record(AttemptVerdict{Attempt: attempt.Number, Resolved: verdict.Resolved, Error: verdict.Reason()})
		if verdict.Resolved {
			return pursue.Done()
		}
		reason := "the official SWE-bench verifier marked the current patch unresolved"
		if verdict.Err != nil {
			reason += ": " + verdict.Reason()
		}
		return pursue.Retry(verifiedFeedback(task, attempt, reason))
	}))
}

func swebenchContextThreader(_ context.Context, _ pursue.Attempt, next runner.TaskSpec, decision pursue.Decision) runner.TaskSpec {
	// The worktree already carries the previous attempt's edits. Replaying the
	// full transcript between long attempts blows the prompt budget, so verified
	// eval re-drives with the verifier feedback only and lets the agent inspect
	// the current files when it needs detail. verifiedFeedback prepends the
	// original problem statement so the model doesn't lose the task description.
	next.Context = nil
	if decision.Feedback != "" {
		next.Prompt = decision.Feedback
	}
	return next
}

func verifiedFeedback(task Task, attempt pursue.Attempt, reason string) string {
	var b strings.Builder
	// The original problem statement is restated so the model retains the task
	// description when the threader drops the prior transcript.
	b.WriteString("## Original task\n\n")
	b.WriteString(task.Problem)
	if task.Hints != "" {
		b.WriteString("\n\n## Hints\n\n")
		b.WriteString(task.Hints)
	}
	fmt.Fprintf(&b, "\n\n## Verification after attempt %d\n\n", attempt.Number)
	fmt.Fprintf(&b, "Result: %s.\n\n", reason)
	b.WriteString("Revise the existing workspace patch. Focus on production/source files only; do not edit tests or fixtures. ")
	b.WriteString("Use the verifier signal below as the target, inspect the code as needed, and then produce a corrected patch.\n")
	if len(task.FailToPass) > 0 {
		b.WriteString("\nFail-to-pass tests expected by the grader:\n")
		for _, name := range task.FailToPass {
			fmt.Fprintf(&b, "- %s\n", name)
		}
	}
	return b.String()
}

func (d *ZarlcodeDriver) verifyPatch(ctx context.Context, instanceID string, attempt int, diff string) (evaluator.Verdict, error) {
	if len(diff) > maxVerifyDiffBytes {
		return evaluator.Verdict{}, fmt.Errorf("patch is %d bytes, over the %d-byte verifier cap", len(diff), maxVerifyDiffBytes)
	}

	ctx, cancel := context.WithTimeout(ctx, d.verifyTimeout())
	defer cancel()

	workDir, cleanup, err := d.verifyWorkDir(instanceID, attempt)
	if err != nil {
		return evaluator.Verdict{}, err
	}
	defer cleanup()

	driver := "zarlcode-verify"
	runID := fmt.Sprintf("verify-%s-%d", sanitizeRunID(instanceID), attempt)
	predsPath := filepath.Join(workDir, fmt.Sprintf("predictions-%s.json", runID))
	if err := writeVerifyPrediction(predsPath, driver, instanceID, diff); err != nil {
		return evaluator.Verdict{}, err
	}
	cmd := exec.CommandContext(ctx, d.verifyPython(),
		"-m", "swebench.harness.run_evaluation",
		"--dataset_name", d.verifyDataset(),
		"--predictions_path", predsPath,
		"--max_workers", strconv.Itoa(d.verifyWorkers()),
		"--run_id", runID,
	)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return evaluator.Verdict{}, fmt.Errorf("invoke: %w\nstderr: %s", err, stderr.String())
	}
	summaryPath := filepath.Join(workDir, fmt.Sprintf("%s.%s.json", driver, runID))
	verdicts, err := evaluator.ParseSummary(summaryPath)
	if err != nil {
		return evaluator.Verdict{}, err
	}
	v, ok := verdicts[instanceID]
	if !ok {
		return evaluator.Verdict{}, fmt.Errorf("summary missing verdict for %s", instanceID)
	}
	return v, nil
}

func (d *ZarlcodeDriver) verifyWorkDir(instanceID string, attempt int) (string, func(), error) {
	if d.VerifyWorkDir == "" {
		dir, err := os.MkdirTemp("", "swebench-verify-*")
		if err != nil {
			return "", func() {}, fmt.Errorf("mkdir tempdir: %w", err)
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}
	dir := filepath.Join(d.VerifyWorkDir, sanitizeRunID(instanceID), fmt.Sprintf("attempt-%d", attempt))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", func() {}, fmt.Errorf("mkdir verify workdir: %w", err)
	}
	return dir, func() {}, nil
}

func (d *ZarlcodeDriver) verifyDataset() string {
	if d.VerifyDataset != "" {
		return d.VerifyDataset
	}
	return "SWE-bench/SWE-bench_Multilingual"
}

func (d *ZarlcodeDriver) verifyPython() string {
	if d.VerifyPython != "" {
		return d.VerifyPython
	}
	return "python3"
}

func (d *ZarlcodeDriver) verifyTimeout() time.Duration {
	if d.VerifyTimeout > 0 {
		return d.VerifyTimeout
	}
	return defaultVerifyTimeout
}

func (d *ZarlcodeDriver) verifyWorkers() int {
	if d.VerifyWorkers > 0 {
		return d.VerifyWorkers
	}
	return 1
}

func writeVerifyPrediction(path, driver, instanceID, diff string) error {
	return evaluator.WritePredictions(path, []evaluator.Prediction{{
		InstanceID:      instanceID,
		ModelPatch:      diff,
		ModelNameOrPath: driver,
	}})
}

func sanitizeRunID(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, s)
	return strings.Trim(s, "-")
}

// reportProvider names the provider for the eval report. Falls back to
// the built client's own Name() when no explicit pin was given (so the
// registry default — llamacpp — shows up rather than an empty column).
func (d *ZarlcodeDriver) reportProvider() string {
	if d.Provider != "" {
		return d.Provider
	}
	if d.prov != nil {
		return d.prov.Name()
	}
	return ""
}

// taskPrompt is the user turn handed to the agent: the problem
// statement plus optional hints. Intentionally minimal — the agent
// discovers test names, fixtures, and build status through tool use.
// Appending FAIL_TO_PASS names here was observed to make the model
// author new test fixtures, contaminating the grader's expected diff.
// writeTranscript persists one task's full agent message history to
// <dir>/<id>.json so empty or failed patches can be diagnosed after the
// worktree is cleaned up. Best-effort: a failure logs and is otherwise
// ignored — a missing transcript must never fail the run.
func writeTranscript(dir, id string, msgs []llm.Message) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "zarlcode driver: transcript dir %q: %v\n", dir, err)
		return
	}
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "zarlcode driver: marshal transcript %s: %v\n", id, err)
		return
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "zarlcode driver: write transcript %s: %v\n", path, err)
	}
}

func taskPrompt(t Task) string {
	var b strings.Builder
	b.WriteString(t.Problem)
	if t.Hints != "" {
		b.WriteString("\n\n## Hints\n\n")
		b.WriteString(t.Hints)
	}
	return b.String()
}

// resetClient is a dedicated client for the llama-server reset POST: a
// short timeout and redirects disabled so a 3xx can't bounce the request
// off the loopback allowlist to an unintended host.
var resetClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// resetLlamacppSlot POSTs to rawURL to evict the llama-server's KV cache
// slot. The canonical endpoint
// (http://localhost:8081/slots/0?action=erase) requires the server to
// have been started with --slot-save-path; on a non-2xx it surfaces the
// response body so the operator can diagnose without grepping logs.
//
// Unless allowRemote is set, the URL must resolve to loopback so a
// misconfigured or hostile reset URL can't be used as an SSRF POST
// against an arbitrary host.
func resetLlamacppSlot(ctx context.Context, rawURL string, allowRemote bool) error {
	if !allowRemote {
		if err := requireLoopbackURL(rawURL); err != nil {
			return err
		}
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := resetClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
}

// requireLoopbackURL rejects a reset URL whose host is not loopback. A
// bare hostname like "localhost" is allowed; an IP must sit in 127.0.0.0/8
// or be ::1. Anything that resolves elsewhere (or doesn't parse) is
// refused with an actionable message pointing at AllowRemoteResetURL.
func requireLoopbackURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse reset url: %w", err)
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("reset url host %q is not loopback; set AllowRemoteResetURL to permit a remote llama-server", host)
}
