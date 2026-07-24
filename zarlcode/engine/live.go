package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/hooks"
	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	computertools "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
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
	// nestedInstructionIndex is the lazy-loaded index of nested AGENTS.md /
	// CLAUDE.md files below the workspace root, enumerable via list_instructions
	// and loadable via load_instruction. Set by reloadInstructions and
	// snapshotted per turn alongside instructionDocs.
	nestedInstructionIndex []instructions.NestedDoc

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
	// computer owns the lazy browser session backing computer_observe and
	// computer_act. The session is process-local and closed with the LiveRunner.
	computer *liveComputer

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
	l.computer = &liveComputer{owner: l}
	// Only populate the sink seams when a real sink was supplied; callers	// disable events by passing a nil LiveSink.
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
	// spill dir and tear down any live MCP/browser connections. Both are
	// best-effort — a non-nil return is informational.
	defer func() {
		if l.truncator != nil {
			_ = l.truncator.Cleanup()
		}
		l.mu.Lock()
		mcp := l.mcp
		computer := l.computer
		l.mu.Unlock()
		if mcp != nil {
			_ = mcp.CloseAll()
		}
		if computer != nil {
			_ = computer.Close()
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
func buildLiveCompactor(engine string, window int, prov llm.Provider, model string, state compact.StateProvider, wsRoot string) compact.Compactor {
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
	case "handover":
		if prov != nil {
			return compact.NewHandover(prov, model, state, handoverWriter(wsRoot))
		}
	}
	return compact.NewTiered(window)
}

// handoverWriter persists a handover document under <wsRoot>/.zarlcode/handovers
// as a timestamped markdown file, returning its path. Empty wsRoot (or a nil
// return) leaves the handover in-context only — the reseed still works, just
// without a durable artifact.
func handoverWriter(wsRoot string) compact.HandoverWriter {
	if wsRoot == "" {
		return nil
	}
	return func(_ context.Context, doc string) (string, error) {
		dir := filepath.Join(home.WorkspaceDir(wsRoot), "handovers")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("handovers dir: %w", err)
		}
		path := filepath.Join(dir, time.Now().Format("2006-01-02-150405")+".md")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			return "", fmt.Errorf("write %q: %w", path, err)
		}
		return path, nil
	}
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
	switch {
	case headless:
		// Headless stays strict for eval determinism, whatever the user set.
		testEdit = guardrails.NewTestEditStrict()
	case l.settings != nil:
		switch l.settings.TestEditMode(l.parentContext()) {
		case guardModeAdvisory:
			testEdit = guardrails.NewTestEditAdvisory()
		case guardModeStrict:
			testEdit = guardrails.NewTestEditStrict()
		}
	}
	// Shared fan-out caps from coderunner so they can't drift from the eval;
	// StandardGuardrailDeps wires no language verifier by default. SkillLookup
	// is the TUI-only extra. DecomposeJudge arms the constrained-verdict judge
	// when the decompose_judge setting is on; nil keeps the deterministic path.
	deps := coderunner.StandardGuardrailDeps(l.ws.Root(), testEdit)
	deps.SkillLookup = l.catalog
	deps.DecomposeJudge = l.decomposeJudge()
	// plan_first gate: refuse the first workspace-changing call until update_plan
	// has run. Off unless the user opts in (weak/local-model profile). PlanTool
	// matches what sourceWithDeps registers against the live plan store.
	if l.settings != nil && l.settings.PlanFirst(l.parentContext()) {
		deps.PlanFirst = true
		deps.PlanTool = code.ToolNameUpdatePlan
	}
	// fanout_fanoutCap > 0 overrides the per-tool exploration caps uniformly (bounds
	// context growth on small-window local models). 0 keeps the eval-shared
	// StandardFanoutLimits.
	if l.settings != nil {
		if fanoutCap := l.settings.FanoutCap(l.parentContext()); fanoutCap > 0 {
			deps.FanoutLimits = map[tools.ToolName]int{
				code.ToolNameLs:   fanoutCap,
				code.ToolNameGrep: fanoutCap,
				code.ToolNameGlob: fanoutCap,
			}
		}
		// Per-task spawn_agent budget, applied after the discovery-cap block
		// (which replaces the whole map and would otherwise drop it). 0 flows
		// through as "uncapped" since the guardrail treats a non-positive limit
		// as unbounded.
		if deps.FanoutLimits == nil {
			deps.FanoutLimits = map[tools.ToolName]int{}
		}
		deps.FanoutLimits[spawn.ToolNameSpawnAgent] = l.settings.SpawnFanoutCap(l.parentContext())
		deps.ReadBeforeWriteMode = l.settings.ReadBeforeWriteMode(l.parentContext())
		// Strict profile follows the sandbox: ON (the kernel is the real
		// boundary) keeps the static shell/read-before-write blocks; OFF is
		// the operator's opt-in to an unconfined, high-trust mode, so they
		// relax rather than provoke python-based evasion. Gated on the
		// setting (not whether Landlock materialised) so a sandbox that
		// failed to start stays strict. The ZARLCODE_SANDBOX env override
		// wins where set, matching the launch path.
		sandboxOn := l.settings.ShellSandbox(l.parentContext())
		if enabled, ok := sandbox.EnvOverride(); ok {
			sandboxOn = enabled
		}
		deps.ShellLenient = l.settings.ShellGuardLenient(l.parentContext(), sandboxOn)
		// Always-on guardrails the user can drop from the chain. Names come
		// from the guardrails package so they can't drift from Name().
		if l.settings.ShellGuardOff(l.parentContext()) {
			// "off" removes the shell guardrail outright — a high-trust opt-in
			// beyond "lenient" (which keeps it and only relaxes the steers).
			// ShellLenient is then moot since the guardrail is gone.
			deps.Disabled = append(deps.Disabled, guardrails.NameShellPolicy)
		}
		if !l.settings.ImprovementGuard(l.parentContext()) {
			deps.Disabled = append(deps.Disabled, guardrails.NameImprovementLoop)
		}
		if !l.settings.SkillHints(l.parentContext()) {
			deps.Disabled = append(deps.Disabled, guardrails.NameSkillHint)
		}
	}
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

	// Optional tool clusters, gated by settings so a lean local-model setup can
	// shrink the surface. Resolved per turn (toggling re-shapes the next turn's
	// roster). Background off → bash registers foreground-only and the
	// bash_output/stop_process/list_processes trio is omitted (pm = nil).
	enableWeb, enableMCP, enableBackground, enableProgrammatic := true, true, true, false
	programParallel := 0
	if l.settings != nil {
		sctx := l.parentContext()
		enableWeb = l.settings.EnableWeb(sctx)
		enableMCP = l.settings.EnableMCP(sctx)
		enableBackground = l.settings.EnableBackground(sctx)
		enableProgrammatic = l.settings.ProgrammaticTools(sctx)
		programParallel = l.settings.ProgrammaticParallelCalls(sctx)
	}
	pmArg := l.pm
	if !enableBackground {
		pmArg = nil
	}
	coderunner.RegisterStandardTools(reg, l.ws, pmArg,
		coderunner.WithToolSandbox(l.sandbox),
		coderunner.WithToolEnv(toolEnv),
	)

	// update_plan isn't in the standard set (it needs a UI-hooked PlanStore);
	// register it here bound to the live store so the agent's structured plan
	// flows to the plan overlay. Registered before GuardedSource so it runs
	// under the same guardrail chain as every other tool.
	if l.planStore != nil {
		_ = reg.Register(code.NewUpdatePlanTool(l.planStore))
	}

	// web_search isn't part of the standard code tool set (it needs an
	// external SearXNG endpoint), so register it here when one is configured.
	// Registered before GuardedSource so it runs under the same guardrail
	// chain as every other tool.
	if enableWeb && searxngURL != "" {
		_ = reg.Register(search.New(searxngURL))
	}

	if l.computer != nil {
		computertools.Register(reg, l.computer, l.computer)
	}

	// web_fetch — HTTP GET fast path, chromedp fallback for JS-heavy pages.
	// Gated with web_search under the enable_web cluster toggle.
	if enableWeb {
		ft := fetch.New()
		if l.settings != nil {
			if cp := l.settings.ChromeBinPath(l.parentContext()); cp != "" {
				ft.WithChromeBinPath(cp)
			}
		}
		_ = reg.Register(ft)
	}

	if l.catalog != nil {
		// Skill/agent catalogue tools are active lookup surfaces. The catalogues are
		// intentionally NOT inlined into prompts; local models should not pay that
		// token cost unless the user asks for a skill/sub-agent or the task clearly
		// needs one.
		_ = reg.Register(NewListSkillsTool(l.catalog))
		_ = reg.Register(NewLoadSkillTool(l.catalog))
		_ = reg.Register(NewListAgentsTool(l.catalog))
	}

	// Nested instruction docs (non-root AGENTS.md / CLAUDE.md) are discoverable
	// via list_instructions and loadable via load_instruction, mirroring the
	// skill/agent lazy-loading surface. Registered before GuardedSource so
	// they run under the same guardrail chain.
	_ = reg.Register(NewListInstructionsTool(l.instructionNestedSnapshot))
	_ = reg.Register(NewLoadInstructionTool(l.ws.Root(), l.instructionNestedSnapshot))

	// MCP: the connect/disconnect/list tools mutate the persistent registry
	// (so connections survive across turns), and every tool already exposed by
	// a connected server is merged onto this turn's registry. Registered before
	// GuardedSource so MCP tool calls run under the same guardrail chain. Gated
	// by the enable_mcp cluster toggle.
	if l.mcp != nil && enableMCP {
		_ = reg.Register(dynamic.NewMCPConnect(l.mcp))
		_ = reg.Register(dynamic.NewMCPDisconnect(l.mcp))
		_ = reg.Register(dynamic.NewMCPList(l.mcp))
	}

	// Diff recorder: capture write/edit mutations and stream
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

	guarded, _, err := coderunner.GuardedSource(base, deps, pipeline, ToolNameListSkills, ToolNameListAgents, ToolNameListInstructions, ToolNameLoadInstruction)
	if err != nil {
		return nil, nil, fmt.Errorf("guarded tool source: %w", err)
	}
	if enableProgrammatic {
		programLimits := programtools.Limits{}
		if programParallel > 0 {
			programLimits.MaxParallelCalls = programParallel
			if programParallel > 20 {
				programLimits.MaxToolCalls = programParallel
			}
		}
		programSource, err := programtools.NewSource(guarded,
			programtools.WithPolicy(coderunner.ProgrammaticReadPolicy()),
			programtools.WithLimits(programLimits),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("program tool source: %w", err)
		}
		guarded = programSource
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
	r, _, err := l.buildTurnWithSource(l.source, runner.WithContextBreakdown())
	return r, err
}
func (l *LiveRunner) buildHeadlessTurn(extraOpts ...options.Option[runner.Runner]) (*runner.Runner, error) {
	r, _, err := l.buildTurnWithSource(l.headlessSource, extraOpts...)
	return r, err
}
func (l *LiveRunner) buildTurnWithSource(sourceFn func(string) (tools.Source, *tools.Registry, error), extraOpts ...options.Option[runner.Runner]) (*runner.Runner, bool, error) {
	// Snapshot the (re-pointable) run target for this turn. The PLAN flag
	// is still read live by prompt/source closures so a mid-turn toggle
	// gates the next dispatch.
	l.mu.Lock()
	tgt := l.target
	settings := l.settings
	thinking := l.thinkingEnabledForLocked(tgt)
	l.mu.Unlock()
	prov, model, window, searxngURL := tgt.Provider, tgt.Model, tgt.Window, tgt.SearxngURL
	reserve, maxIter, spawnMaxIter, spawnDepth := tgt.Reserve, tgt.MaxIter, tgt.SpawnMaxIter, tgt.SpawnDepth
	if l.catalog != nil {
		l.catalog.Reload(l.ws.Root())
	}
	l.reloadInstructions()

	// Resolve the compaction engine (and its optional LLM target) live, so
	// a settings change takes effect on the next turn without a restart.
	engine, compactProv, compactModel := compact.EngineTiered, prov, model
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

	// Per-turn settings tuning: tool-result truncation caps (mutate the shared
	// truncator — turns are serialized) and sampling temperature. Read live so a
	// settings change applies next turn without a restart.
	var temperature float32
	var streamIdle time.Duration
	autoCompact := true
	if settings != nil {
		sctx := l.parentContext()
		l.truncator.MaxBytes = settings.ToolResultMaxBytes(sctx)
		l.truncator.MaxLines = settings.ToolResultMaxLines(sctx)
		temperature = settings.Temperature(sctx)
		streamIdle = settings.ResponseTimeout(sctx)
		autoCompact = settings.AutoCompact(sctx)
	}

	opts := coderunner.StandardOptions(coderunner.Tuning{
		Model:         model,
		MaxIterations: maxIter,
		ContextWindow: window,
		StreamIdle:    streamIdle,
	})
	var visible tools.Source
	opts = append(opts,
		runner.WithSteerer(l.queue),
		runner.WithPrompt(l.promptFunc(func() tools.Source { return visible })),
		runner.WithResultTruncator(l.truncator),
		runner.WithTemperature(temperature),
	)
	// Arm the auto-compactor only in auto mode. In manual mode the user
	// compacts on demand (CompactNow builds its own compactor, so it still
	// works) and the cockpit warns as pressure crosses the trigger.
	if autoCompact {
		opts = append(opts, runner.WithCompactor(coderunner.StandardCompactor(
			buildLiveCompactor(engine, window, compactProv, compactModel, l, l.ws.Root()), window, reserve)))
	}
	if l.sink != nil {
		opts = append(opts, runner.WithSink(l.sink))
	}

	// Wrap the guarded source with the PLAN-mode filter, reading the flag
	// live so toggling mid-run gates the next dispatch.
	src, reg, err := sourceFn(searxngURL)
	if err != nil {
		return nil, false, err
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
	return r, thinking, nil
}

// ManualCompactionResult reports the effect of a user-triggered conversation
// compaction.
type ManualCompactionResult struct {
	MessagesBefore int
	MessagesAfter  int
	BytesTrimmed   int
	Engine         string
}

// CompactNow immediately applies the configured compaction engine to the live
func (l *LiveRunner) CompactNow(ctx context.Context) (ManualCompactionResult, error) {
	if l == nil {
		return ManualCompactionResult{}, errors.New("compact now: live runner is nil")
	}
	l.mu.Lock()
	tgt := l.target
	settings := l.settings
	l.mu.Unlock()

	prov, model, window := tgt.Provider, tgt.Model, tgt.Window
	if window <= 0 {
		window = LiveContextWindow
	}
	engineName, compactProv, compactModel := compact.EngineTiered, prov, model
	if settings != nil {
		engineName = settings.CompactEngine(ctx)
		compactProv, compactModel = settings.CompactorProvider(ctx, prov, model)
	}
	return l.conv.compactNow(ctx, buildLiveCompactor(engineName, window, compactProv, compactModel, l, l.ws.Root()), l.sink)
}

func (l *LiveRunner) RunTurn(prompt string) error {
	return l.RunTurnWithAttachments(prompt, nil)
}

func (l *LiveRunner) RunTurnWithAttachments(prompt string, attachments []llm.ContentPart) error {
	return l.conv.runSpecWithSetup(runner.TaskSpec{Prompt: prompt, Attachments: attachments}, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		r, thinking, err := l.buildTurnWithSource(l.source, runner.WithContextBreakdown())
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(l.parentContext())
		done := make(chan struct{})
		l.mu.Lock()
		l.turnCancel = cancel
		l.turnDone = done
		l.mu.Unlock()
		return func(spec runner.TaskSpec) runner.TaskResult {
			defer func() {
				cancel()
				close(done)
				l.mu.Lock()
				if l.turnDone == done {
					l.turnCancel = nil
					l.turnDone = nil
				}
				l.mu.Unlock()
			}()
			spec.Thinking = thinking
			return r.Run(ctx, spec)
		}, nil
	})
}

func (l *LiveRunner) thinkingEnabledForLocked(tgt RunTarget) bool {
	if l.settings == nil || l.settings.Registry == nil {
		return false
	}
	return l.settings.Registry.Capabilities(tgt.Spec.Name, tgt.Model).SupportsThinking
}
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}
