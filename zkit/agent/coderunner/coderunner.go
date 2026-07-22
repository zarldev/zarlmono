// Package coderunner is the canonical assembly of the zarlcode coding
// agent loop: the standard workspace tool set, the production guardrail
// chain, and the tuned runner options — extracted so every consumer
// drives a behaviorally identical loop.
//
// Two consumers share it today:
//
//   - the zarlcode TUI (interactive + headless sessions), and
//   - the SWE-bench eval driver (swebench-eval).
//
// Before this package the TUI assembled all three inline in
// zarlcode/tui, and the eval framework shelled out to the zarlcode
// binary to inherit them. The binary round-trip is what this package
// removes: eval now builds the same loop in-process from the same
// helpers the TUI uses, so "what I tested in the TUI" and "what eval
// submitted" cannot drift.
//
// The package deliberately does NOT own provider construction, the
// event sink, the steerer, the system prompt, or the diff capture —
// those are consumer-specific glue. It owns the three things that
// MUST be identical across consumers for fidelity: which tools the
// agent sees, how the guardrail chain is shaped, and how the loop is
// tuned.
package coderunner

import (
	"regexp"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ProgrammaticReadPolicy returns the conservative v1 allowlist for nested
// program tool calls. It intentionally combines exact names with the current
// spec capability flags so dynamic or reclassified tools fail closed.
func ProgrammaticReadPolicy() program.Policy {
	allowed := map[tools.ToolName]struct{}{
		code.ToolNameRead:         {},
		code.ToolNameGrep:         {},
		code.ToolNameGlob:         {},
		code.ToolNameLs:           {},
		code.ToolNameFileMap:      {},
		code.ToolNameRetrieveCode: {},
		tools.ToolNameWebSearch:   {},
		tools.ToolNameWebFetch:    {},
		"list_skills":             {},
		"list_agents":             {},
		"list_instructions":       {},
	}
	return func(spec tools.ToolSpec) bool {
		if spec.Name == program.ToolName || spec.ChangesWorkspace() {
			return false
		}
		_, ok := allowed[spec.Name]
		return ok
	}
}

// RegisterStandardTools registers the standard workspace code-tool set onto
// reg: the file tools (write, edit), the read/search tools (read, grep, ls,
// glob), bash + process-management tools, and the path-locked plan archive
// tools (save_plan / save_plan_append). The set is kept lean — one tool per
// job, no competing variants. Deliberately NOT registered: write_append (write
// + edit cover authoring; >256KB single files are rare).
//
// The labelled variants are the default, matching the TUI; JSON siblings (the
// format-switch) and the interactive-only tools (update_plan with its UI hook,
// list_skills/list_agents, dynamic + MCP + home tools, web search) stay a
// caller concern.
//
// pm may be nil; the bash tool is then registered without a process
// manager (background-process tools degrade to "no manager" errors).
// For a SWE-bench worktree the process manager is real but the agent
// rarely backgrounds anything.
//
// WithToolSandbox confines the bash tool's foreground commands; it
// covers only this registration — background commands spawn through
// pm, which exists before this call, so the caller hands the SAME
// sandboxer to code.NewProcessManager via code.WithProcessSandbox.
// The hosting binary must call sandbox.ExecShim first thing in main
// or a sandboxed command re-runs the whole program as its "shell".
func RegisterStandardTools(reg *tools.Registry, ws code.Workspace, pm *code.ProcessManager, opts ...ToolsOption) {
	var cfg toolsConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	var bashOpts []code.BashOption
	if cfg.sandbox != nil {
		bashOpts = append(bashOpts, code.WithSandbox(cfg.sandbox))
	}
	if len(cfg.env) > 0 {
		bashOpts = append(bashOpts, code.WithEnv(cfg.env))
	}
	var readOpts []code.ReadOption
	if cfg.unrestrictedReads || cfg.sandbox == nil {
		readOpts = append(readOpts, code.WithUnrestrictedReads())
	}
	if pm != nil {
		_ = reg.Register(code.NewBashTool(ws, append(bashOpts, code.WithProcessManager(pm))...))
		_ = reg.Register(code.NewBashOutputTool(pm))
		_ = reg.Register(code.NewStopProcessTool(pm))
		_ = reg.Register(code.NewListProcessesTool(pm))
	} else {
		_ = reg.Register(code.NewBashTool(ws, bashOpts...))
	}
	_ = reg.Register(code.NewReadFileHLTool(ws, readOpts...))
	_ = reg.Register(code.NewWriteTool(ws))
	_ = reg.Register(code.NewEditFileHLTool(ws))
	_ = reg.Register(code.NewGrepTool(ws, readOpts...))
	_ = reg.Register(code.NewLsTool(ws, readOpts...))
	_ = reg.Register(code.NewGlobTool(ws, readOpts...))
	_ = reg.Register(code.NewFileMapTool(ws, readOpts...))
	_ = reg.Register(code.NewRetrieveCodeTool(ws, readOpts...))
	_ = reg.Register(code.NewSavePlanTool(ws))
	_ = reg.Register(code.NewSavePlanAppendTool(ws))
}

// toolsConfig collects RegisterStandardTools options.
type toolsConfig struct {
	sandbox           code.Sandboxer
	env               map[string]string
	unrestrictedReads bool
}

// ToolsOption tunes RegisterStandardTools.
type ToolsOption func(*toolsConfig)

// WithToolSandbox confines shell commands behind sb (kernel-level
// filesystem allow-list and optional network denial — see
// zkit/agent/sandbox). Nil is a no-op so callers can pass through an
// optional sandboxer unconditionally.
func WithToolSandbox(sb code.Sandboxer) ToolsOption {
	return func(c *toolsConfig) { c.sandbox = sb }
}

// WithToolEnv appends child-process environment variables to shell commands
// spawned by the bash tool.
func WithToolEnv(env map[string]string) ToolsOption {
	return func(c *toolsConfig) { c.env = env }
}

// WithUnrestrictedReads allows read-only tools (read, ls, grep, glob) to
// access paths outside the workspace root. Mutating tools remain rooted to the
// workspace. Callers normally rely on the default derived from sandbox mode
// (sandbox off => unrestricted reads); this option exists for tests and custom
// embeddings.
func WithUnrestrictedReads() ToolsOption {
	return func(c *toolsConfig) { c.unrestrictedReads = true }
}

// RegisterSpawnTool registers the spawn_agent tool on reg, wired to parent
// (children recurse through the same loop) with a recursion ceiling and an
// optional sub-agent iteration cap.
//
// Call it AFTER building the runner. This breaks the parent↔source cycle —
// the tool needs the runner, but the runner is built from the source the
// tool lives in — because a Registry enumerates its tools lazily
// (Registry.Tools is evaluated per Run), so a spawn registered once the
// runner exists is visible to the next turn's schema and dispatch.
//
// maxDepth: a positive value caps recursion at that depth; a negative value
// keeps spawn's built-in default (depth 1); 0 disables spawning, in which
// case the tool is NOT registered at all (no point surfacing a tool that
// always refuses).
//
// spawnMaxIter: a positive value caps child iterations; 0 (or negative)
// leaves the spawn tool's built-in default (inherit from the runner).
// Named sub-agents and the grammar planner stay a caller concern — this is
// the minimal shared wiring every consumer can share.
func RegisterSpawnTool(reg *tools.Registry, parent *runner.Runner, maxDepth, spawnMaxIter int) {
	if maxDepth == 0 {
		return // spawning explicitly disabled — don't surface the tool
	}
	_ = reg.Register(spawn.New(parent,
		spawn.WithMaxDepth(maxDepth),
		spawn.WithSpawnMaxIterations(spawnMaxIter),
		spawn.WithModeToolPolicy(SpawnModePolicy()),
	))
}

// SpawnModePolicy is the work-mode tool policy enforced by the spawn tool
// on a sub-agent's Run. Exported so a consumer that builds its spawn tool
// by hand — the TUI wires named-agent resolution and a grammar planner that
// the minimal RegisterSpawnTool doesn't — arms the SAME enforcement via
// spawn.WithModeToolPolicy. Without it explore/verify modes degrade to
// advisory prompt text with no real tool gating. The policy:
//   - explore: read-only — no file mutation and no shell (bash can mutate).
//   - verify:  may run tests/builds via bash, but not edit files.
//   - implement (and any unset/unknown mode): the full tool surface.
//
// Mutation is read from each tool's self-declared capability flags, so the
// classification lives with the tool (no hardcoded name list to drift) and
// a runtime-registered mutating tool that declares the flag is gated too.
// explore blocks anything that can touch the workspace (ChangesWorkspace —
// file edits AND bash); verify gates only on Mutates (a file edit), so bash
// stays callable there to run the tests without any name special-case.
//
// External tools are conservative by default: MCP-discovered tools are
// wrapped by tools.NewRemoteTool, which hardcodes Mutates:true (MCP's
// ToolDef carries no read-only annotation we can trust), and the dynamic
// new/build/unregister meta-tools declare Mutates:true because they
// mutate the registry — so both are gated out of explore/verify rather
// than slipping through on a zero-value spec. The one self-attested
// surface is dynamic *binary* tools: a binary that mutates must set the
// flag in its --describe spec to be honoured.
//
// Verify-mode bash is additionally policed by the shell guardrail's
// verify profile (shellpolicy.DecideVerify): write-target detection plus
// a mutating-command deny-list block sed -i / rm / git checkout / go mod
// and friends, so verify means "run tests and report", not just "no
// tool-mediated mutation". Static analysis can't see through eval or
// interpreter one-liners, so the profile hardens the boundary rather
// than sandboxing it.
func SpawnModePolicy() func(spawn.SpawnMode, tools.ToolSpec) bool {
	return func(mode spawn.SpawnMode, spec tools.ToolSpec) bool {
		switch mode {
		case spawn.SpawnModeExplore:
			return !spec.ChangesWorkspace()
		case spawn.SpawnModeVerify:
			return !spec.Mutates
		default: // implement, "", or anything unexpected → no restriction
			return true
		}
	}
}

// Tuning carries the per-run loop knobs. The constants the production
// loop is tuned with (adaptive keep-recent window, finalize-warn
// threshold, the empty-response correction) are baked into
// StandardOptions, not exposed here — they're the shared invariant, not
// a per-consumer dial. Only the genuinely per-run values live on Tuning.
type Tuning struct {
	// Model selects the chat template via templates.PickByModel. Empty
	// falls back to the runner's default (Qwen3).
	Model string

	// MaxIterations caps the agent loop. Zero leaves the runner's
	// configured default in place (and per-task TaskSpec.MaxIterations
	// can still override).
	MaxIterations int

	// ToolConcurrency caps concurrent tool dispatch per batch. Zero/one
	// is sequential.
	ToolConcurrency int

	// ContextWindow is the model's context window in tokens. When > 0 it
	// arms the token-pressure force-compact at TokenPressureFraction of the
	// window — the provider's tokenizer catches the effective coherence wall
	// (where local models lose tool-call discipline) that byte-pressure
	// heuristics miss. Zero leaves the force-path off.
	ContextWindow int

	// StreamIdle overrides the no-chunk stall watchdog (see StreamIdleTimeout).
	// Zero keeps the shared 90s default so every consumer stays identical
	// unless a caller deliberately dials it — e.g. a slow local model or
	// connection that legitimately pauses longer than 90s between chunks.
	StreamIdle time.Duration
}

// Tuning constants shared by every consumer. Exported so a consumer
// that builds its options by hand (rather than via StandardOptions)
// can still match the production loop exactly.
const (
	// AdaptiveKeepTargetTokens / AdaptiveKeepMin / AdaptiveKeepMax are
	// the token-budget-aware keep-recent window: walk the tail keeping
	// messages until the running estimate hits the target, clamped to
	// [min, max]. Tuned for 32k-window models; larger families can
	// afford more but this is a safe floor.
	AdaptiveKeepTargetTokens = 8000
	AdaptiveKeepMin          = 2
	AdaptiveKeepMax          = 12

	// FinalizeWarnThreshold is the iterations-remaining count at which
	// the "wrap up" nudge fires, giving the model a clear last-chance
	// signal before it hits the cap with no final answer.
	FinalizeWarnThreshold = 5

	// TokenPressureFraction is the share of the context window at which the
	// runner force-compacts (skips the Prober gate, shrinks keepRecent to 1).
	// 0.6 brackets the observed coherence walls of the local models this
	// loop targets — Qwen3.6-35B degrades near 0.5 of its 131k window,
	// Llama-3.1-70B near 0.6 — while staying late enough not to thrash
	// frontier models that hold longer. A consumer wanting an absolute
	// per-model threshold can set runner.WithTokenPressureCompact directly.
	TokenPressureFraction = 0.6

	// IterationTimeout caps a single iteration's LLM call + stream drain;
	// StreamIdleTimeout caps the gap between consecutive stream chunks.
	// These are shared stall-watchdogs, NOT per-run dials — baking them in
	// keeps every consumer's stall detection identical, so a model that
	// pauses N seconds mid-stream is killed the same way in the TUI and in
	// eval (previously the TUI inherited the runner's 60s default while eval
	// set 90s, so the two diverged on a load-bearing timeout).
	//
	// StreamIdleTimeout (no chunk for 90s) is the REAL stall detector. The
	// iteration cap is a coarse wall-clock backstop — it can't tell a
	// healthy long generation from a degenerate thinking dump, and under a
	// saturated box its timer can even fire late. So the real bounding of a
	// runaway is done by MaxCompletionTokens (a hard token ceiling) and
	// ThinkingOnlyBudgetBytes (a content-aware early-cut), NOT by stretching
	// this timeout. 5m is the backstop; the token/thinking budgets cut well
	// before it on the degenerate path.
	IterationTimeout  = 5 * time.Minute
	StreamIdleTimeout = 90 * time.Second

	// MaxCompletionTokens caps a single generation's output tokens — a
	// hard, deterministic ceiling that doesn't depend on the model honoring
	// enable_thinking or on timer scheduling under load. Sized well under
	// what the 5m timeout would allow (~40k tokens at local decode rates)
	// so a runaway is bounded by token count, not wall clock, while leaving
	// ample room for a legitimately long reasoning+answer turn.
	MaxCompletionTokens = 32768

	// ThinkingOnlyBudgetBytes cuts an iteration that has streamed only
	// reasoning past this many bytes (~24k tokens) with no visible content
	// or tool call — the stuck-thinking loop. Set below the token ceiling
	// so the degenerate path is caught early with a corrective nudge rather
	// than a hard truncate; a healthy turn emits real output long before
	// this and is never touched.
	ThinkingOnlyBudgetBytes = 96 * 1024
)

// emptyResponseCorrection is the canonical "you produced nothing"
// nudge. When a thinking-budget-capped model burns its tokens inside
// <think> and leaves no reply or tool call, the runner would otherwise
// exit the loop empty-handed; this message re-drives one iteration.
// Coding-agent-flavoured (mentions tool calls and file changes), which
// is why it overrides the runner's generic default.
const emptyResponseCorrection = "Your previous response had no user-visible answer. Produce a concise final answer now. " +
	"If you performed tool calls or changed files, summarize what you did and mention any remaining issues. " +
	"If more work is needed, call the appropriate tool. Do not emit only reasoning."

// DefaultEmptyResponseDetector returns the production empty-turn
// corrector: one correction per run, thinking disabled on the retry so
// a thinking-only model is forced to emit a visible answer.
func DefaultEmptyResponseDetector() runner.EmptyResponseDetector {
	return runner.EmptyResponseDetector{
		Message:                emptyResponseCorrection,
		DisableThinkingOnRetry: true,
		MaxCorrections:         1,
	}
}

// DefaultMalformedToolCallDetector returns the production guard for a tool call
// the model emitted as malformed JSON that no recovery path could parse — one
// re-emit correction per run before the turn is allowed to stand as prose.
func DefaultMalformedToolCallDetector() runner.MalformedToolCallDetector {
	return runner.MalformedToolCallDetector{MaxCorrections: 1}
}

// DefaultTurnQuality is the canonical content-side guardrail chain: catch a
// malformed/unrecovered tool call first (re-emit), then a fully empty turn
// (make progress). Both are bounded per run.
func DefaultTurnQuality() runner.TurnQuality {
	return runner.ChainTurnQuality{
		DefaultMalformedToolCallDetector(),
		DefaultEmptyResponseDetector(),
	}
}

// StandardOptions returns the canonical loop-behaviour options every
// consumer shares: chat template, iteration cap, tool concurrency,
// adaptive keep-recent, the per-iteration watchdogs, the empty-turn
// corrector, and the finalize-warn nudge.
//
// It returns ONLY the behaviour options. The plumbing — sink, prompt
// source, steerer, compactor, result truncator, progress updater — is
// consumer-specific and appended by the caller. Compose like:
//
//	opts := coderunner.StandardOptions(tuning)
//	opts = append(opts,
//	    runner.WithSink(mySink),
//	    runner.WithPrompt(myPrompt),
//	    runner.WithCompactor(myGate),
//	    runner.WithTools(mySource),
//	)
//	r := runner.New(client, opts...)
func StandardOptions(t Tuning) []options.Option[runner.Runner] {
	streamIdle := StreamIdleTimeout
	if t.StreamIdle > 0 {
		streamIdle = t.StreamIdle
	}
	opts := []options.Option[runner.Runner]{
		runner.WithAdaptiveKeepRecent(AdaptiveKeepTargetTokens, AdaptiveKeepMin, AdaptiveKeepMax),
		runner.WithTurnQuality(DefaultTurnQuality()),
		runner.WithFinalizeWarn(runner.FinalizeWarn{RemainingThreshold: FinalizeWarnThreshold}),
		runner.WithIterationTimeout(IterationTimeout),
		runner.WithStreamIdleTimeout(streamIdle),
		runner.WithMaxTokens(MaxCompletionTokens),
		runner.WithThinkingBudget(ThinkingOnlyBudgetBytes),
	}
	if t.Model != "" {
		opts = append(opts, runner.WithTemplate(templates.PickByModel(t.Model)))
	}
	if t.MaxIterations > 0 {
		opts = append(opts, runner.WithMaxIterations(t.MaxIterations))
	}
	if t.ToolConcurrency > 0 {
		opts = append(opts, runner.WithToolConcurrency(t.ToolConcurrency))
	}
	if t.ContextWindow > 0 {
		opts = append(opts, runner.WithTokenPressureCompact(t.ContextWindow, TokenPressureFraction))
	}
	return opts
}

// ToolCallCount returns how many tool calls the assistant made across
// messages. Only assistant turns carry real tool calls, so it filters on
// role — a naive sum over every message's ToolCalls double-counts if a
// non-assistant turn ever carries them. Both consumers' tool-call telemetry
// goes through here so the TUI and the eval can't report different numbers
// for the same run.
func ToolCallCount(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == llm.RoleAssistant {
			n += len(m.ToolCalls)
		}
	}
	return n
}

// guardrailRejectionRe matches the stable prefix failedFromGuard stamps on a
// guardrail rejection's user-facing error: `guardrail "<name>": ...`.
var guardrailRejectionRe = regexp.MustCompile(`guardrail "([^"]+)"`)

// GuardrailRejectionCounts counts guardrail rejections per guardrail name
// across a run's transcript, by scanning tool-role messages for the stable
// rejection prefix. It is the shared telemetry both the eval's ablation
// report and any TUI surface read, so the two can't count differently.
// Advisory guardrails that never reject (skill-hint, the improvement loop)
// don't appear here — this measures enforcement, not advice.
func GuardrailRejectionCounts(messages []llm.Message) map[string]int {
	var out map[string]int
	for _, m := range messages {
		if m.Role != llm.RoleTool {
			continue
		}
		for _, match := range guardrailRejectionRe.FindAllStringSubmatch(m.Content, -1) {
			if out == nil {
				out = map[string]int{}
			}
			out[match[1]]++
		}
	}
	return out
}

// StandardCompactor wraps inner in the production pressure gate: the runner
// consults it every iteration but it only fires Compact when observed usage is
// within reserve tokens of window. Both consumers share the gate shape so the
// "compact only under real pressure" trigger can't drift — only the inner
// engine (the TUI's tiered+executive compactor vs the eval's bare tiered one)
// and the per-consumer window/reserve differ.
func StandardCompactor(inner compact.Compactor, window, reserve int) *compact.PressureGated {
	return &compact.PressureGated{Inner: inner, Window: window, Reserve: reserve}
}

// StandardFanoutLimits are the per-tool repeat-call caps the guardrail
// chain enforces — the shared invariant both consumers run with. Returned
// fresh (the map is mutable) so a caller can tune one entry without mutating
// the canonical set. Read is intentionally not capped here: it is the
// primary way an agent gets precise evidence into its own context, and repeated
// identical reads are already memoized per task.
func StandardFanoutLimits() map[tools.ToolName]int {
	return map[tools.ToolName]int{
		code.ToolNameLs:          20,
		code.ToolNameGrep:        30,
		code.ToolNameGlob:        20,
		spawn.ToolNameSpawnAgent: StandardSpawnFanoutCap,
	}
}

// StandardSpawnFanoutCap bounds how many spawn_agent calls a single task may
// issue before the fanout guardrail starts refusing them. Without it a model
// can fan out sub-agents unbounded ("researcher + reviewer + coder" is the
// intended handful, per the tool's own description). Zero in the fanout map
// would mean unbounded, so this stays positive; consumers that want it off
// remove the entry.
const StandardSpawnFanoutCap = 8

// StandardGuardrailDeps returns the guardrail dependencies every consumer
// shares — the fan-out caps — rooted at root with the given test-edit policy
// (advisory for an interactive session, strict for a headless run or the eval
// grader). No language verifier is wired by default: runtime checks should
// reflect explicit user/workspace config, not baked-in language assumptions.
// here is why a cap can't drift between the TUI and the eval.
func StandardGuardrailDeps(root string, testEdit guardrails.Guardrail) guardrails.Deps {
	return guardrails.Deps{
		WorkspaceRoot: root,
		FanoutLimits:  StandardFanoutLimits(),
		TestEdit:      testEdit,
	}
}

// pureReadTools are the side-effect-free tools the MemoSource caches

// pureReadTools are the side-effect-free tools the MemoSource caches
// per task: identical args → identical answer within one task, so a
// repeat call skips dispatch. Mirrors the TUI's pure set minus the
// interactive-only list_skills/list_agents (passed as extraPure by the
// TUI). The bucket dies with the task ID, so no cross-turn staleness.
var pureReadTools = []tools.ToolName{
	code.ToolNameRead, code.ToolNameLs, code.ToolNameGrep, code.ToolNameGlob,
}

// GuardedSource arms the production guardrail chain over base, exactly
// as the TUI does: the supplied pipeline wrappers run first (diff
// recorder → format switch → mode filter → depth filter, each optional),
// then schema + the post-schema guardrails (shell, skill-hint,
// decompose, fanout, test-edit, improvement) from deps, then a
// per-task MemoSource over the pure read tools.
//
// extraPure adds consumer-specific pure tools to the memoized set (the
// TUI passes its list_skills/list_agents). It returns the armed source
// plus the guardrail names (for observability surfaces like /tools).
func GuardedSource(
	base tools.Source,
	deps guardrails.Deps,
	pipeline sourcechain.Pipeline,
	extraPure ...tools.ToolName,
) (tools.Source, []string, error) {
	var ledger runner.TaskCallLedger
	if deps.ReadBeforeWriteMode != guardrails.ReadBeforeWriteOff {
		ledger = runner.NewMemoryTaskCallLedger()
		deps.ExtraEvidence = ledger
	}
	chain, err := sourcechain.New(pipeline.Wrap(base), deps)
	if err != nil {
		return nil, nil, err
	}
	pure := append(append([]tools.ToolName{}, pureReadTools...), extraPure...)
	if ledger != nil {
		memo := runner.NewMemoSourceWithLedger(chain.Source, runner.PureTools(pure...), ledger)
		return memo, chain.GuardrailNames, nil
	}
	memo := runner.NewMemoSource(chain.Source, runner.PureTools(pure...))
	return memo, chain.GuardrailNames, nil
}
