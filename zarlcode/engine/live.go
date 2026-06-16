package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zarlcode/hooks"
	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
	"github.com/zarldev/zarlmono/zkit/options"
)

const (
	LiveContextWindow = 32768 // sizes the compactor (tokens)
	liveReserveTokens = 512   // held back from the window
)

// LiveSink is what the live engine needs from its event sink: the runner's
// event stream plus the diff-recorder and plan side-channels. The TUI's
// *teasink.Sink satisfies it; defining it here keeps the engine free of the
// bubbletea-coupled concrete sink.
type LiveSink interface {
	runner.EventSink
	DiffEvent(diffrecorder.DiffEvent)
	PlanUpdated(code.Plan)
}

// planEmitter is the slice of LiveSink the plan store needs.
type planEmitter interface {
	PlanUpdated(code.Plan)
}

// LiveRunner builds and drives real agent runs against a provider, delivering
// events through the sink. Construct one, wire its sink to the program
// (sink.SetSend(program.Send)), then hand it to the TUI's run handler.
//
// It uses coderunner's standard code tool set plus the production guardrail
// chain (shell, logging, skill-hint, decompose, fanout, test-edit, improvement)
// and the diff recorder — the same core assembly swebench drives, so that
// shared behaviour can't drift. The surrounding tool surface and the
// advisory-vs-strict test-edit policy are configured per consumer (interactive
// is advisory; headless/eval is strict). A conversation threads history across
// turns, and a pressure-gated compactor keeps long chats inside the window.
type LiveRunner struct {
	ws    code.Workspace
	sink  LiveSink
	conv  conversation
	queue *queueState

	// mu guards target (the hot-swappable run target) and appCtx so the
	// settings overlay can re-point them while a run may be in flight; a turn
	// snapshots target under the lock at start, so an update takes effect on the
	// next turn. Read it via RunTarget, write it via the SetX setters.
	mu     sync.Mutex
	appCtx context.Context
	target RunTarget

	// turnCancel cancels the context driving the current turn's runner.Run.
	// Set under mu before entering the run loop, cleared under mu on exit.
	// Nil when no turn is in flight — safe for the TUI to call unconditionally.
	turnCancel context.CancelFunc
	turnDone   chan struct{}

	// settings is the prefs handle used to resolve the compaction engine (and
	// its optional LLM provider/model) live at turn start. nil keeps the
	// default tiered compactor.
	settings *Settings

	// planStore is an engine-side adapter for update_plan: SetPlan pushes a
	// PlanUpdatedMsg through the sink so the TUI writes the canonical Session.Plan,
	// while retaining a runner-local copy for executive compaction. UI panes must
	// read Session.Plan, not this adapter.
	planStore *livePlanStore

	// catalog is the live skills/agents/hooks snapshot used by prompts,
	// load_skill, list_* tools, skill-hint guardrails, named spawn_agent
	// routing, and the per-turn hook guardrail.
	catalog *RuntimeCatalog

	// instructions is the live AGENTS.md / CLAUDE.md snapshot included in system
	// prompts. It is reloaded at top-level turn start so edits take effect without
	// restarting zarlcode.
	instructionDocs []instructions.Document
	instructionErrs []error

	// truncator tail-caps oversized tool results and spills the full text to
	// disk so a follow-up bash can grep it. One shared instance across every
	// turn-runner and sub-agent runner (it's concurrency-safe), owned here and
	// Cleanup'd in Close so spills don't accumulate in TempDir.
	truncator *runner.SpillingTruncator

	// earlyStopCommand, when set, turns a headless run into a "keep going
	// until this command passes" loop: the harness watcher runs it
	// (diff-gated, in the workspace root) and stops the attempt the moment it
	// exits zero. Empty disables early stop. Snapshotted per run under mu.
	earlyStopCommand []string

	// verifyCommand + verifyAttempts arm the headless verified re-drive
	// loop: after each attempt the command runs as the world-checking
	// oracle (coderunner.CommandGoal); a non-zero exit feeds its output
	// back and re-drives with the full transcript, up to verifyAttempts.
	// Empty command or attempts <= 1 keeps the single-shot shape.
	verifyCommand  string
	verifyAttempts int

	// pm manages background bash processes (bash background=true, bash_output,
	// stop_process, list_processes). Shared across turns so a server started in
	// one turn is visible/stoppable in the next. nil registers bash without
	// process management (foreground only).
	pm *code.ProcessManager

	// sandbox confines shell commands (foreground bash here, background
	// via pm's own copy) behind the kernel allow-list. nil runs
	// unsandboxed — pre-sandbox behaviour.
	sandbox code.Sandboxer
	// toolEnv is appended to bash subprocess environments (foreground and
	// background), e.g. sudo askpass integration.
	toolEnv map[string]string

	// mcp holds live MCP server connections, bound to mcpHost. Persistent so a
	// server connected in one turn stays connected; its discovered tools are
	// merged into each turn's registry. nil disables mcp_connect/disconnect/list.
	mcp     *dynamic.MCPRegistry
	mcpHost *tools.Registry
}

// livePlanStore adapts code.PlanStore for the runner. It is not the UI source
// of truth; SetPlan broadcasts PlanUpdatedMsg and the Bubble Tea update loop
// writes the canonical Session.Plan. The local copy exists only so compaction
// can read the latest plan from the runner side.
type livePlanStore struct {
	sink planEmitter
	mu   sync.Mutex
	plan code.Plan
	// version increments on every SetPlan so a turn can tell whether the live
	// plan changed during its own run (vs inheriting a stale plan from earlier
	// work) before enforcing completion-time plan hygiene.
	version uint64
}

func (p *livePlanStore) SetPlan(pl code.Plan) {
	p.mu.Lock()
	p.plan = clonePlan(pl)
	p.version++
	p.mu.Unlock()
	if p.sink != nil {
		p.sink.PlanUpdated(pl)
	}
}

func (p *livePlanStore) GetPlan() code.Plan {
	pl, _ := p.Snapshot()
	return pl
}

func (p *livePlanStore) Snapshot() (code.Plan, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePlan(p.plan), p.version
}

func clonePlan(pl code.Plan) code.Plan {
	out := pl
	if len(pl.Steps) > 0 {
		out.Steps = append([]code.PlanStep(nil), pl.Steps...)
	}
	return out
}

// NewLiveRunner wires the provider, workspace, and sink for live runs. The
// compactor window defaults to LiveContextWindow until SetContextWindow
// overrides it with the provider's real window.
func NewLiveRunner(prov llm.Provider, ws code.Workspace, sink LiveSink, model string) *LiveRunner {
	l := &LiveRunner{
		ws: ws,
		target: RunTarget{
			Provider: prov,
			Spec:     ProviderSpec{Model: model},
			Model:    model,
			Window:   LiveContextWindow,
		},
		queue:     newQueueState(),
		planStore: &livePlanStore{},
		catalog:   newRuntimeCatalog(ws.Root()),
		truncator: &runner.SpillingTruncator{Prefix: "zarlcode-"},
	}
	// Only populate the sink seams when a real sink was supplied; callers
	// disable events by passing a nil LiveSink.
	if sink != nil {
		l.sink = sink
		l.planStore.sink = sink
	}
	return l
}

// SetContext wires the application lifetime into interactive turns. A nil
// context clears the app binding and RunFn falls back to context.Background in
// tests. Production launch passes the zapp/Bubble Tea context so quit/signal
// cancellation reaches in-flight providers and tools.
func (l *LiveRunner) SetContext(ctx context.Context) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.appCtx = ctx
	l.mu.Unlock()
}

func (l *LiveRunner) parentContext() context.Context {
	if l == nil {
		return context.Background()
	}
	l.mu.Lock()
	ctx := l.appCtx
	l.mu.Unlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Close cancels the current turn and waits for any RunFn command to return.
// It is intended to run before sink/process/settings closers so active tools
// observe cancellation while their dependencies still exist.
func (l *LiveRunner) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	// Once the in-flight turn has drained: remove this session's tool-result
	// spill dir and tear down any live MCP connections (stdio servers would
	// otherwise leave orphaned child processes). Both best-effort — a non-nil
	// return is informational.
	defer func() {
		if l.truncator != nil {
			_ = l.truncator.Cleanup()
		}
		l.mu.Lock()
		mcp := l.mcp
		l.mu.Unlock()
		if mcp != nil {
			_ = mcp.CloseAll()
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	l.CancelTurn()
	l.mu.Lock()
	done := l.turnDone
	l.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("live runner close: %w", ctx.Err())
	}
}

// SetSettingsHandle wires the prefs handle so RunFn can resolve the
// compaction engine (compact_engine / compact_provider / compact_model) live.
func (l *LiveRunner) SetSettingsHandle(s *Settings) {
	l.mu.Lock()
	l.settings = s
	l.mu.Unlock()
}

// SetProcessManager wires the background-process manager so the bash tool can
// background commands and the bash_output / stop_process / list_processes
// tools are registered.
func (l *LiveRunner) SetProcessManager(pm *code.ProcessManager) {
	l.mu.Lock()
	l.pm = pm
	l.mu.Unlock()
}

// SetSandbox wires the shell-command sandbox for foreground bash. The
// caller wires the same instance into the process manager
// (code.WithProcessSandbox) so background commands match.
func (l *LiveRunner) SetSandbox(sb code.Sandboxer) {
	l.mu.Lock()
	l.sandbox = sb
	l.mu.Unlock()
}

// SetToolEnv wires environment variables appended to bash subprocesses.
func (l *LiveRunner) SetToolEnv(env map[string]string) {
	l.mu.Lock()
	l.toolEnv = cloneStringMap(env)
	l.mu.Unlock()
}

// SetMCP wires the persistent MCP registry (and the host registry its
// connected tools land on) so mcp_connect / mcp_disconnect / mcp_list are
// available and connected servers' tools are merged into each turn's registry.
func (l *LiveRunner) SetMCP(reg *dynamic.MCPRegistry, host *tools.Registry) {
	l.mu.Lock()
	l.mcp, l.mcpHost = reg, host
	l.mu.Unlock()
}

// Plan satisfies compact.StateProvider for the executive engine, surfacing the
// live update_plan state so the briefing can carry the current step list.
func (l *LiveRunner) Plan() []compact.PlanStep {
	if l == nil || l.planStore == nil {
		return nil
	}
	plan := l.planStore.GetPlan()
	out := make([]compact.PlanStep, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		out = append(out, compact.PlanStep{Title: s.Text, Status: s.Status.String()})
	}
	return out
}

// WorkingFiles / TopTools also satisfy compact.StateProvider; v2's live runner
// doesn't yet track those, so they stay empty — the executive briefing still
// summarises the older history, just without that per-section detail.
func (l *LiveRunner) WorkingFiles() []compact.FileTouch { return nil }
func (l *LiveRunner) TopTools() []compact.ToolUsage     { return nil }

// buildLiveCompactor builds the compactor for the resolved engine. summary /
// executive need an LLM provider; without one they fall back to tiered so a
// misconfigured engine never breaks compaction. structural and tiered are
// no-LLM; anything unknown is tiered (the quiet progressive default).
func buildLiveCompactor(engine string, window int, prov llm.Provider, model string, state compact.StateProvider) compact.Compactor {
	switch engine {
	case "structural":
		return compact.NewStructural()
	case "summary":
		if prov != nil {
			return compact.NewSummary(prov, model)
		}
	case "executive":
		if prov != nil {
			return compact.NewExecutive(prov, model, state)
		}
	}
	return compact.NewTiered(window)
}

// QueueInput appends user text to the live-turn injection queue. The running
// top-level runner drains it between iterations via runner.WithSteerer.
func (l *LiveRunner) QueueInput(text string) int {
	if l == nil || l.queue == nil {
		return 0
	}
	n, _ := l.queue.Append(text)
	return n
}

func (l *LiveRunner) popQueuedInput() (llm.Message, bool) {
	if l == nil || l.queue == nil {
		return llm.Message{}, false
	}
	return l.queue.Pop()
}

// History snapshots the conversation context for persistence.
func (l *LiveRunner) History() []llm.Message {
	if l == nil {
		return nil
	}
	return l.conv.snapshot()
}

// RestoreHistory replaces the conversation context when the intro resumes a
// saved session (or starts fresh with an empty history).
func (l *LiveRunner) RestoreHistory(history []llm.Message) {
	if l == nil {
		return
	}
	l.conv.restore(history)
}

// ClearHistory clears the conversation context threaded into the next turn.
func (l *LiveRunner) ClearHistory() {
	if l == nil {
		return
	}
	l.conv.restore(nil)
}

// SetPlanMode toggles PLAN mode on the next dispatch / turn. PLAN restricts
// the runner to read-only tools and swaps in a planning prompt. Read live by
// the source filter, so flipping mid-run takes effect on the next tool call.
func (l *LiveRunner) SetPlanMode(on bool) {
	l.mu.Lock()
	l.target.Plan = on
	l.mu.Unlock()
}

// isPlan reports the current PLAN-mode flag (read by the mode-filtered
// source on each dispatch).
func (l *LiveRunner) isPlan() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target.Plan
}

// SetContextWindow overrides the compactor's context window (tokens) — set
// it to the provider's real window so compaction fires at the true budget,
// not the conservative default. Ignored for non-positive values.
func (l *LiveRunner) SetContextWindow(tokens int) {
	if tokens > 0 {
		l.mu.Lock()
		l.target.Window = tokens
		l.mu.Unlock()
	}
}

// SetSearxngURL enables the web_search tool against the given SearXNG
// endpoint (resolved from settings/env/default by the caller). Empty leaves
// web_search unregistered. Snapshotted per turn like the run target.
func (l *LiveRunner) SetSearxngURL(url string) {
	l.mu.Lock()
	l.target.SearxngURL = url
	l.mu.Unlock()
}

// SetLimits applies the run-budget settings (reserve tokens, max iterations,
// sub-agent spawn max iterations, sub-agent spawn depth) from the settings
// pane. reserve/maxIter/spawnMaxIter zero keep the compiled-in defaults;
// spawnDepth 0 disables spawning, >0 caps recursion at that depth.
// Snapshotted per turn like the rest of the run target.
func (l *LiveRunner) SetLimits(reserve, maxIter, spawnMaxIter, spawnDepth int) {
	l.mu.Lock()
	l.target.Reserve = reserve
	l.target.MaxIter = maxIter
	l.target.SpawnMaxIter = spawnMaxIter
	l.target.SpawnDepth = spawnDepth
	l.mu.Unlock()
}

// SetEarlyStopCommand configures the headless early-stop check: a command run
// (diff-gated) in the workspace root whose zero exit stops the attempt early.
// A nil/empty slice disables it. Copied so the caller can't mutate it under
// the lock; snapshotted again per run.
func (l *LiveRunner) SetEarlyStopCommand(cmd []string) {
	l.mu.Lock()
	l.earlyStopCommand = append([]string(nil), cmd...)
	l.mu.Unlock()
}

// SetVerifyLoop configures the headless verified re-drive: cmd is the shell
// command that acts as the verification oracle (run via `sh -c` in the
// workspace root), attempts caps the agent attempts. Empty cmd or
// attempts <= 1 disables the loop. Snapshotted per run like the run target.
func (l *LiveRunner) SetVerifyLoop(cmd string, attempts int) {
	l.mu.Lock()
	l.verifyCommand = strings.TrimSpace(cmd)
	l.verifyAttempts = attempts
	l.mu.Unlock()
}

// SetProvider hot-swaps the run target — the provider built for the newly
// selected backend + its model. Used by the settings overlay so a provider
// change takes effect without a restart; the next turn picks it up.
func (l *LiveRunner) SetProvider(prov llm.Provider, model string) {
	if prov == nil {
		return
	}
	l.mu.Lock()
	l.target.Provider = prov
	l.target.Model = model
	l.target.Spec.Model = model
	l.mu.Unlock()
}

// SetProviderSpec hot-swaps the run target and records the resolved provider
// spec, so named agents that only override `model` can rebuild the active
// backend with that model.
func (l *LiveRunner) SetProviderSpec(prov llm.Provider, spec ProviderSpec) {
	if prov == nil {
		return
	}
	l.mu.Lock()
	l.target.Provider = prov
	l.target.Model = spec.Model
	l.target.Spec = spec
	l.mu.Unlock()
}

// SetModel updates only the model name on the current provider.
// The change takes effect on the next turn without a rebuild.
func (l *LiveRunner) SetModel(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.target.Model = name
	l.target.Spec.Model = name
}

// CancelTurn cancels the context driving the current turn's runner.Run.
// Safe to call from any goroutine; a nil or already-fired cancel is a no-op.
// Returns true when a turn was in flight and the cancel was delivered.
func (l *LiveRunner) CancelTurn() bool {
	l.mu.Lock()
	cancel := l.turnCancel
	l.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// guardrailDeps is the single source of the production guardrail configuration:
// the verifiers, fan-out caps, and test-edit policy. source() wires these into
// the chain and the inspector reports them from the same place, so a change
// here can't drift from what the inspector shows.
func (l *LiveRunner) guardrailDeps() guardrails.Deps {
	return l.guardrailDepsFor(false)
}

func (l *LiveRunner) headlessGuardrailDeps() guardrails.Deps {
	return l.guardrailDepsFor(true)
}

func (l *LiveRunner) guardrailDepsFor(headless bool) guardrails.Deps {
	var testEdit guardrails.Guardrail
	if headless {
		testEdit = guardrails.NewTestEditStrict()
	}
	// Shared invariant (GoVerifier + fan-out caps) from coderunner so it can't
	// drift from the eval; SkillLookup is the TUI-only extra. DecomposeJudge
	// arms the constrained-verdict judge when the decompose_judge setting is
	// on; nil keeps the deterministic path.
	deps := coderunner.StandardGuardrailDeps(l.ws.Root(), testEdit)
	deps.SkillLookup = l.catalog
	deps.DecomposeJudge = l.decomposeJudge()
	return deps
}

// decomposeJudge resolves the optional constrained-verdict judge for the
// decompose guardrail. Resolved fresh per turn like the compactor, so
// toggling decompose_judge (or re-pointing judge_provider / judge_model)
// takes effect on the next turn without a restart. nil — judge off, no
// settings handle, or no provider to run it on — keeps the guardrail's
// deterministic advisory path.
func (l *LiveRunner) decomposeJudge() guardrails.VerdictJudge {
	l.mu.Lock()
	settings := l.settings
	active := l.target.Provider
	spec := l.target.Spec
	l.mu.Unlock()
	if settings == nil {
		return nil
	}
	prov := settings.DecomposeJudgeProvider(l.parentContext(), active, spec)
	if prov == nil {
		return nil
	}
	return guardrails.NewLLMVerdictJudge(prov)
}

// source registers the standard tools and arms the production guardrail
// chain + diff recorder over them. Falls back to the bare registry if the
// chain can't be built. Built fresh per run so guardrail state (decompose
// counters, improvement loop) resets per turn.
//
// It returns the wrapped source AND the underlying registry: the caller
// late-registers the spawn tool onto the registry after building the runner
// (the registry enumerates lazily, so it's visible to the turn's schema).
func (l *LiveRunner) source(searxngURL string) (tools.Source, *tools.Registry, error) {
	return l.sourceWithDeps(searxngURL, l.guardrailDeps())
}

func (l *LiveRunner) headlessSource(searxngURL string) (tools.Source, *tools.Registry, error) {
	return l.sourceWithDeps(searxngURL, l.headlessGuardrailDeps())
}

func (l *LiveRunner) sourceWithDeps(searxngURL string, deps guardrails.Deps) (tools.Source, *tools.Registry, error) {
	reg := tools.NewRegistry()
	l.mu.Lock()
	toolEnv := cloneStringMap(l.toolEnv)
	l.mu.Unlock()
	coderunner.RegisterStandardTools(reg, l.ws, l.pm,
		coderunner.WithToolSandbox(l.sandbox),
		coderunner.WithToolEnv(toolEnv),
	)

	// update_plan isn't in the standard set (it needs a UI-hooked PlanStore);
	// register it here bound to the live store so the agent's structured plan
	// flows to the plan overlay. Registered before GuardedSource so it runs
	// under the same guardrail chain as every other tool.
	if l.planStore != nil {
		reg.Register(code.NewUpdatePlanTool(l.planStore))
	}

	// web_search isn't part of the standard code tool set (it needs an
	// external SearXNG endpoint), so register it here when one is configured.
	// Registered before GuardedSource so it runs under the same guardrail
	// chain as every other tool.
	if searxngURL != "" {
		reg.Register(search.New(searxngURL))
	}

	// web_fetch is always registered — no external configuration needed.
	// HTTP GET is the fast path; chromedp fallback for JS-heavy pages.
	ft := fetch.New()
	if l.settings != nil {
		if cp := l.settings.ChromeBinPath(l.parentContext()); cp != "" {
			ft.WithChromeBinPath(cp)
		}
	}
	reg.Register(ft)

	if l.catalog != nil {
		reg.Register(NewListSkillsTool(l.catalog))
		reg.Register(NewLoadSkillTool(l.catalog))
		reg.Register(NewListAgentsTool(l.catalog))
	}

	// MCP: the connect/disconnect/list tools mutate the persistent registry
	// (so connections survive across turns), and every tool already exposed by
	// a connected server is merged onto this turn's registry. Registered before
	// GuardedSource so MCP tool calls run under the same guardrail chain.
	if l.mcp != nil {
		reg.Register(dynamic.NewMCPConnect(l.mcp))
		reg.Register(dynamic.NewMCPDisconnect(l.mcp))
		reg.Register(dynamic.NewMCPList(l.mcp))
	}

	// Diff recorder: capture write/edit/apply_patch mutations and stream
	// them to the timeline (ordered with tool events via the sink pump).
	var pipeline sourcechain.Pipeline
	if l.sink != nil {
		pipeline = sourcechain.NewPipeline(
			sourcechain.WithDiffRecorder(func(src tools.Source) tools.Source {
				return diffrecorder.NewWithEventSink(src, l.ws.Root(), diffrecorder.NewClassifier(), l.sink.DiffEvent)
			}),
		)
	}
	base := tools.Source(reg)
	if l.mcpHost != nil {
		base = newCompositeSource(base, l.mcpHost)
	}

	// User-defined command hooks ride the same chain as the production
	// guardrails, appended last so they only see calls the production set
	// already admitted. Compiled fresh per turn from the catalog snapshot, so
	// editing a hook file takes effect on the next turn like skills do.
	if hg, err := hooks.NewGuardrail(l.ws.Root(), l.catalog.Hooks()); err != nil {
		return nil, nil, fmt.Errorf("compile hooks: %w", err)
	} else if !hg.Empty() {
		deps.Extra = append(deps.Extra, hg)
	}

	guarded, _, err := coderunner.GuardedSource(base, deps, pipeline, ToolNameListSkills, ToolNameListAgents)
	if err != nil {
		return nil, nil, fmt.Errorf("guarded tool source: %w", err)
	}
	return guarded, reg, nil
}

// buildTurn assembles the runner for one turn: a snapshot of the
// re-pointable run target, the guarded standard tool set, the shared tuned
// options, the live-resolved compactor, and the late-registered spawn tool.
// RunFn (interactive) calls this; RunHeadless calls buildHeadlessTurn. Both
// route through buildTurnWithSource so the loop body is shared and cannot
// drift — they differ only in guardrail policy (interactive test-edit is
// advisory; headless/eval is strict).
func (l *LiveRunner) buildTurn() (*runner.Runner, error) {
	// Interactive only: the cockpit's context-window graph consumes the
	// per-iteration breakdown. Headless/eval (buildHeadlessTurn) leave it off.
	return l.buildTurnWithSource(l.source, runner.WithContextBreakdown())
}

func (l *LiveRunner) buildHeadlessTurn(extraOpts ...options.Option[runner.Runner]) (*runner.Runner, error) {
	return l.buildTurnWithSource(l.headlessSource, extraOpts...)
}

func (l *LiveRunner) buildTurnWithSource(sourceFn func(string) (tools.Source, *tools.Registry, error), extraOpts ...options.Option[runner.Runner]) (*runner.Runner, error) {
	// Snapshot the (re-pointable) run target for this turn. The PLAN flag
	// is still read live by prompt/source closures so a mid-turn toggle
	// gates the next dispatch.
	l.mu.Lock()
	tgt := l.target
	settings := l.settings
	l.mu.Unlock()
	prov, model, window, searxngURL := tgt.Provider, tgt.Model, tgt.Window, tgt.SearxngURL
	reserve, maxIter, spawnMaxIter, spawnDepth := tgt.Reserve, tgt.MaxIter, tgt.SpawnMaxIter, tgt.SpawnDepth
	if l.catalog != nil {
		l.catalog.Reload(l.ws.Root())
	}
	l.reloadInstructions()

	// Resolve the compaction engine (and its optional LLM target) live, so
	// a settings change takes effect on the next turn without a restart.
	engine, compactProv, compactModel := "tiered", prov, model
	if settings != nil {
		ctx := l.parentContext()
		engine = settings.CompactEngine(ctx)
		compactProv, compactModel = settings.CompactorProvider(ctx, prov, model)
	}

	// Settings overrides, else the compiled-in defaults.
	if maxIter <= 0 {
		maxIter = 20
	}
	if reserve <= 0 {
		reserve = liveReserveTokens
	}

	opts := coderunner.StandardOptions(coderunner.Tuning{
		Model:         model,
		MaxIterations: maxIter,
		ContextWindow: window,
	})
	var visible tools.Source
	opts = append(opts,
		runner.WithSteerer(l.queue),
		runner.WithPrompt(l.promptFunc(func() tools.Source { return visible })),
		runner.WithCompactor(coderunner.StandardCompactor(
			buildLiveCompactor(engine, window, compactProv, compactModel, l), window, reserve)),
		runner.WithResultTruncator(l.truncator),
	)
	if l.sink != nil {
		opts = append(opts, runner.WithSink(l.sink))
	}

	// Wrap the guarded source with the PLAN-mode filter, reading the flag
	// live so toggling mid-run gates the next dispatch.
	src, reg, err := sourceFn(searxngURL)
	if err != nil {
		return nil, err
	}
	visible = NewModeFilteredSource(src, l.isPlan)
	opts = append(opts, extraOpts...)
	opts = append(opts, runner.WithTurnQuality(newPlanAwareTurnQuality(l.planStore, l.isPlan)))
	opts = append(opts, runner.WithTools(visible))
	r := runner.New(runner.ClientFromProvider(prov), opts...)
	// Late-register spawn onto the base registry now that the parent
	// runner exists (the registry enumerates lazily, so it's visible to
	// this turn). spawnDepth 0 leaves spawning disabled.
	l.registerSpawnTool(reg, r, spawnDepth, spawnMaxIter)
	return r, nil
}

// RunTurn executes one interactive turn to completion: it builds the turn's
// runner, registers cancellation so CancelTurn/Close can interrupt it, and
// threads the result through the conversation so the next turn sees prior
// history. It is the charm-free core that RunFn adapts into a tea.Cmd; callers
// outside the TUI can use it directly. It returns a non-nil error only when
// turn setup fails — a completed or cancelled run returns nil.
func (l *LiveRunner) RunTurn(prompt string) error {
	r, err := l.buildTurn()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(l.parentContext())
	done := make(chan struct{})
	l.mu.Lock()
	l.turnCancel = cancel
	l.turnDone = done
	l.mu.Unlock()
	defer func() {
		close(done)
		l.mu.Lock()
		if l.turnDone == done {
			l.turnCancel = nil
			l.turnDone = nil
		}
		l.mu.Unlock()
	}()
	l.conv.run(prompt, func(spec runner.TaskSpec) runner.TaskResult {
		spec.Thinking = l.thinkingEnabled()
		return r.Run(ctx, spec)
	})
	return nil
}

// thinkingEnabled reports whether the active model supports extended
// thinking, gating the per-run TaskSpec.Thinking toggle on capability so
// reasoning is requested only where the provider can honour it (Anthropic
// extended thinking, Gemini, the OpenAI reasoning line). Capability is
// resolved from the registry — the same provider-side source the cockpit's
// cost basis reads.
func (l *LiveRunner) thinkingEnabled() bool {
	l.mu.Lock()
	provider, model, settings := l.target.Spec.Name, l.target.Model, l.settings
	l.mu.Unlock()
	if settings == nil || settings.Registry == nil {
		return false
	}
	return settings.Registry.Capabilities(provider, model).SupportsThinking
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
