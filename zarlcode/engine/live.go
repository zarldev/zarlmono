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
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
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

type nopLiveSink struct{ runner.NopSink }

func (nopLiveSink) DiffEvent(diffrecorder.DiffEvent) {}
func (nopLiveSink) PlanUpdated(code.Plan)            {}

var _ LiveSink = nopLiveSink{}

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

	// mu guards the hot-swappable run target. A turn snapshots target under the
	// lock at start, so an update takes effect on the next turn.
	mu            sync.Mutex
	target        RunTarget
	promptProfile PromptProfile

	// turnCancel cancels the context driving the current turn's runner.Run.
	// Set under mu before entering the run loop, cleared under mu on exit.
	// Nil when no turn is in flight — safe for the TUI to call unconditionally.
	turnCancel context.CancelFunc
	turnDone   chan struct{}
	// closing is a one-way lifecycle transition. shutdownDone is created by the
	// first Close call; one owned goroutine waits for the active turn, then closes
	// dependencies and publishes shutdownErr to every Close caller.
	closing      bool
	shutdownDone chan struct{}
	shutdownErr  error

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
	// skill_load, list_* tools, skill-hint guardrails, named agent_spawn
	// routing, and the per-turn hook guardrail.
	catalog *RuntimeCatalog

	// instructions is the live AGENTS.md / CLAUDE.md snapshot included in system
	// prompts. It is reloaded at top-level turn start so edits take effect without
	// restarting zarlcode.
	instructionDocs []instructions.Document
	instructionErrs []error
	// nestedInstructionIndex is the lazy-loaded index of nested AGENTS.md /
	// CLAUDE.md files below the workspace root, enumerable via list_instructions
	// and loadable via instruction_load. Set by reloadInstructions and
	// snapshotted per turn alongside instructionDocs.
	nestedInstructionIndex []instructions.NestedDoc
	// operational records bounded session-wide file touches and tool counts for
	// executive/handover compaction state. It stays engine-owned so headless and
	// TUI runs produce the same briefing without Bubble Tea state.
	operational *operationalState

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
	// fetchTool owns the reusable browser renderer used by web_fetch fallbacks.
	// It persists across per-turn registries and closes with the LiveRunner.
	fetchTool *fetch.WebFetchTool

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

func newLivePlanStore() *livePlanStore {
	return &livePlanStore{sink: nopLiveSink{}}
}

func (p *livePlanStore) SetPlan(pl code.Plan) {
	p.mu.Lock()
	p.plan = clonePlan(pl)
	p.version++
	p.mu.Unlock()
	p.sink.PlanUpdated(pl)
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

// WithSettings configures live preference resolution.
func WithSettings(s *Settings) options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.settings = s }
}

// WithProcessManager configures owned background-process access.
func WithProcessManager(pm *code.ProcessManager) options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.pm = pm }
}

// WithSandbox configures foreground shell confinement.
func WithSandbox(sb code.Sandboxer) options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.sandbox = sb }
}

// WithToolEnvironment configures copied subprocess environment additions.
func WithToolEnvironment(env map[string]string) options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.toolEnv = cloneStringMap(env) }
}

// WithMCP configures the persistent MCP registry and discovered-tool host.
func WithMCP(reg *dynamic.MCPRegistry, host *tools.Registry) options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.mcp, l.mcpHost = reg, host }
}

// WithLiveSink overrides the no-op event sink. Passing nil is invalid.
func WithLiveSink(s LiveSink) options.Option[LiveRunner] {
	if s == nil {
		panic("engine: nil live sink")
	}
	return func(l *LiveRunner) { l.sink, l.planStore.sink = s, s }
}

// NewLiveRunner wires the provider, workspace, and sink for live runs. The
// compactor window defaults to LiveContextWindow until SetContextWindow
// overrides it with the provider's real window.
func NewLiveRunner(prov llm.Provider, ws code.Workspace, model string, opts ...options.Option[LiveRunner]) *LiveRunner {
	l := &LiveRunner{
		ws: ws,
		target: RunTarget{
			Provider: prov,
			Spec:     ProviderSpec{Model: model},
			Model:    model,
			Window:   LiveContextWindow,
		},
		promptProfile: PromptProfiles.LEAN,
		queue:         newQueueState(),
		planStore:     newLivePlanStore(),
		catalog:       newRuntimeCatalog(ws.Root()),
		truncator:     &runner.SpillingTruncator{Prefix: "zarlcode-"},
		operational:   newOperationalState(),
		fetchTool:     fetch.New(),
		sink:          nopLiveSink{},
	}
	for _, opt := range opts {
		opt(l)
	}
	l.computer = &liveComputer{owner: l}
	l.planStore.sink = l.sink
	return l
}

// AttachMCP attaches the registry whose notifier already targets this runner's
// queue. It is a composition-time lifecycle transition required by that cycle.
func (l *LiveRunner) AttachMCP(reg *dynamic.MCPRegistry, host *tools.Registry) {
	l.mu.Lock()
	l.mcp, l.mcpHost = reg, host
	l.mu.Unlock()
}

// Close begins the one-way shutdown transition, cancels the active turn, and
// waits for the single owned shutdown operation. A caller deadline only bounds
// that caller's wait: dependencies remain open until the turn actually drains.
// Concurrent and repeated calls observe the same terminal cleanup result.
func (l *LiveRunner) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	l.mu.Lock()
	if !l.closing {
		l.closing = true
		l.shutdownDone = make(chan struct{})
		turnCancel := l.turnCancel
		turnDone := l.turnDone
		shutdownDone := l.shutdownDone
		go l.shutdown(turnDone, shutdownDone)
		if turnCancel != nil {
			turnCancel()
		}
	}
	shutdownDone := l.shutdownDone
	l.mu.Unlock()

	select {
	case <-shutdownDone:
		l.mu.Lock()
		err := l.shutdownErr
		l.mu.Unlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("wait for live runner shutdown: %w", ctx.Err())
	}
}

func (l *LiveRunner) shutdown(turnDone, shutdownDone chan struct{}) {
	if turnDone != nil {
		<-turnDone
	}

	l.mu.Lock()
	mcp := l.mcp
	computer := l.computer
	fetchTool := l.fetchTool
	truncator := l.truncator
	l.mcp, l.mcpHost = nil, nil
	l.computer = nil
	l.fetchTool = nil
	l.truncator = nil
	l.mu.Unlock()

	var errs []error
	if mcp != nil {
		if err := mcp.CloseAll(); err != nil {
			errs = append(errs, fmt.Errorf("close MCP connections: %w", err))
		}
	}
	if computer != nil {
		if err := computer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close computer session: %w", err))
		}
	}
	if fetchTool != nil {
		if err := fetchTool.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close web fetch: %w", err))
		}
	}
	if truncator != nil {
		if err := truncator.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("clean tool spills: %w", err))
		}
	}

	l.mu.Lock()
	l.shutdownErr = errors.Join(errs...)
	close(shutdownDone)
	l.mu.Unlock()
}

// Plan satisfies agentcompact.StateProvider for the executive engine, surfacing the
// live update_plan state so the briefing can carry the current step list.
func (l *LiveRunner) Plan() []agentcompact.PlanStep {
	if l == nil || l.planStore == nil {
		return nil
	}
	plan := l.planStore.GetPlan()
	out := make([]agentcompact.PlanStep, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		out = append(out, agentcompact.PlanStep{Title: s.Text, Status: s.Status.String()})
	}
	return out
}

// WorkingFiles returns the bounded, oldest-to-newest snapshot of files touched
// by successful tool calls in this session. Repeated paths move to the tail with
// their latest action.
func (l *LiveRunner) WorkingFiles() []agentcompact.FileTouch {
	if l == nil || l.operational == nil {
		return nil
	}
	return l.operational.workingFiles()
}

// TopTools returns session-wide tool counts ordered by count descending and
// then name. Executive rendering applies its own display cap.
func (l *LiveRunner) TopTools() []agentcompact.ToolUsage {
	if l == nil || l.operational == nil {
		return nil
	}
	return l.operational.topTools()
}

// Verification returns the latest foreground verification command observed in
// this session.
func (l *LiveRunner) Verification() *agentcompact.VerificationState {
	if l == nil || l.operational == nil {
		return nil
	}
	return l.operational.verificationState()
}

// UnresolvedFailures returns the bounded latest unresolved tool failures.
func (l *LiveRunner) UnresolvedFailures() []agentcompact.FailureState {
	if l == nil || l.operational == nil {
		return nil
	}
	return l.operational.unresolvedFailures()
}

// buildLiveCompactor builds the compactor for the resolved engine. summary /
// executive need an LLM provider; without one they fall back to tiered so a
// misconfigured engine never breaks compaction. structural and tiered are
// no-LLM; anything unknown is tiered (the quiet progressive default).
func buildLiveCompactor(engine string, window int, prov llm.Provider, model string, state agentcompact.StateProvider, wsRoot string) agentcompact.Compactor {
	switch engine {
	case "structural":
		return agentcompact.NewStructural()
	case "summary":
		if prov != nil {
			return agentcompact.NewSummary(prov, model)
		}
	case "executive":
		if prov != nil {
			return agentcompact.NewExecutive(prov, model, state)
		}
	case "handover":
		if prov != nil {
			return agentcompact.NewHandover(prov, model, state, handoverWriter(wsRoot))
		}
	}
	return agentcompact.NewTiered(window)
}

// handoverWriter persists a handover document under <wsRoot>/.zarlcode/handovers
// as a timestamped markdown file, returning its path. Empty wsRoot (or a nil
// return) leaves the handover in-context only — the reseed still works, just
// without a durable artifact.
func handoverWriter(wsRoot string) agentcompact.HandoverWriter {
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

// ApplyTarget atomically updates provider identity and context-window policy.
func (l *LiveRunner) ApplyTarget(update TargetUpdate) {
	if update.Provider == nil {
		return
	}
	l.mu.Lock()
	l.target.Provider = update.Provider
	l.target.Model = update.Spec.Model
	l.target.Spec = update.Spec
	if update.Window > 0 {
		l.target.Window = update.Window
	}
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
func (l *LiveRunner) guardrailDeps(ctx context.Context) guardrails.Deps {
	return l.guardrailDepsFor(ctx, false)
}

func (l *LiveRunner) headlessGuardrailDeps(ctx context.Context) guardrails.Deps {
	return l.guardrailDepsFor(ctx, true)
}

func (l *LiveRunner) guardrailDepsFor(ctx context.Context, headless bool) guardrails.Deps {
	var testEdit guardrails.Guardrail
	switch {
	case headless:
		// Headless stays strict for eval determinism, whatever the user set.
		testEdit = guardrails.NewTestEditStrict()
	case l.settings != nil:
		switch l.settings.TestEditMode(ctx) {
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
	deps.DecomposeJudge = l.decomposeJudge(ctx)
	// plan_first gate: refuse the first workspace-changing call until update_plan
	// has run. Off unless the user opts in (weak/local-model profile). PlanTool
	// matches what sourceWithDeps registers against the live plan store.
	if l.settings != nil && l.settings.PlanFirst(ctx) {
		deps.PlanFirst = true
		deps.PlanTool = code.ToolNameUpdatePlan
	}
	// fanout_fanoutCap > 0 overrides the per-tool exploration caps uniformly (bounds
	// context growth on small-window local models). 0 keeps the eval-shared
	// StandardFanoutLimits.
	if l.settings != nil {
		if fanoutCap := l.settings.FanoutCap(ctx); fanoutCap > 0 {
			deps.FanoutLimits = map[tools.ToolName]int{
				code.ToolNameLs:   fanoutCap,
				code.ToolNameGrep: fanoutCap,
				code.ToolNameGlob: fanoutCap,
			}
		}
		// Per-task agent_spawn budget, applied after the discovery-cap block
		// (which replaces the whole map and would otherwise drop it). 0 flows
		// through as "uncapped" since the guardrail treats a non-positive limit
		// as unbounded.
		if deps.FanoutLimits == nil {
			deps.FanoutLimits = map[tools.ToolName]int{}
		}
		deps.FanoutLimits[spawn.ToolNameAgentSpawn] = l.settings.SpawnFanoutCap(ctx)
		deps.ReadBeforeWriteMode = l.settings.ReadBeforeWriteMode(ctx)
		// Strict profile follows the sandbox: ON (the kernel is the real
		// boundary) keeps the static shell/read-before-write blocks; OFF is
		// the operator's opt-in to an unconfined, high-trust mode, so they
		// relax rather than provoke python-based evasion. Gated on the
		// setting (not whether Landlock materialised) so a sandbox that
		// failed to start stays strict. The ZARLCODE_SANDBOX env override
		// wins where set, matching the launch path.
		sandboxOn := l.settings.ShellSandbox(ctx)
		if enabled, ok := sandbox.EnvOverride(); ok {
			sandboxOn = enabled
		}
		deps.ShellLenient = l.settings.ShellGuardLenient(ctx, sandboxOn)
		// Always-on guardrails the user can drop from the chain. Names come
		// from the guardrails package so they can't drift from Name().
		if l.settings.ShellGuardOff(ctx) {
			// "off" removes the shell guardrail outright — a high-trust opt-in
			// beyond "lenient" (which keeps it and only relaxes the steers).
			// ShellLenient is then moot since the guardrail is gone.
			deps.Disabled = append(deps.Disabled, guardrails.NameShellPolicy)
		}
		if !l.settings.ImprovementGuard(ctx) {
			deps.Disabled = append(deps.Disabled, guardrails.NameImprovementLoop)
		}
		if !l.settings.SkillHints(ctx) {
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
func (l *LiveRunner) decomposeJudge(ctx context.Context) guardrails.VerdictJudge {
	l.mu.Lock()
	settings := l.settings
	active := l.target.Provider
	spec := l.target.Spec
	l.mu.Unlock()
	if settings == nil {
		return nil
	}
	prov := settings.DecomposeJudgeProvider(ctx, active, spec)
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
func (l *LiveRunner) source(ctx context.Context, searxngURL string) (tools.Source, *tools.Registry, error) {
	return l.sourceWithDeps(ctx, searxngURL, l.guardrailDeps(ctx))
}

func (l *LiveRunner) headlessSource(ctx context.Context, searxngURL string) (tools.Source, *tools.Registry, error) {
	return l.sourceWithDeps(ctx, searxngURL, l.headlessGuardrailDeps(ctx))
}

func (l *LiveRunner) sourceWithDeps(ctx context.Context, searxngURL string, deps guardrails.Deps) (tools.Source, *tools.Registry, error) {
	reg := tools.NewRegistry()
	l.mu.Lock()
	toolEnv := cloneStringMap(l.toolEnv)
	l.mu.Unlock()

	// Optional tool clusters, gated by settings so a lean local-model setup can
	// shrink the surface. Resolved per turn (toggling re-shapes the next turn's
	// roster). Background off → bash registers foreground-only and the
	// bash_output/stop_process/list_processes trio is omitted (pm = nil).
	enableWeb, enableMCP, enableBackground, enableProgrammatic := true, true, true, true
	programParallel := 0
	if l.settings != nil {
		sctx := ctx
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
		ft := l.fetchTool
		if ft == nil {
			ft = fetch.New()
		}
		if l.settings != nil {
			ft.ConfigureChromeBinary(l.settings.ChromeBinPath(ctx))
		}
		_ = reg.Register(ft)
	}

	if l.catalog != nil {
		// Skill/agent catalogue tools are active lookup surfaces. The catalogues are
		// intentionally NOT inlined into prompts; local models should not pay that
		// token cost unless the user asks for a skill/sub-agent or the task clearly
		// needs one.
		_ = reg.Register(NewListSkillsTool(l.catalog))
		_ = reg.Register(NewCreateSkillTool(l.catalog))
		_ = reg.Register(NewLoadSkillTool(l.catalog))
		_ = reg.Register(NewListAgentsTool(l.catalog))
	}

	// Nested instruction docs (non-root AGENTS.md / CLAUDE.md) are discoverable
	// via list_instructions and loadable via instruction_load, mirroring the
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
	base = newGuidanceSource(base, l.instructionNestedSnapshot())

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
func (l *LiveRunner) buildTurn(ctx context.Context) (*runner.Runner, error) {
	// Interactive only: the cockpit's context-window graph consumes the
	// per-iteration breakdown. Headless/eval (buildHeadlessTurn) leave it off.
	r, _, _, err := l.buildTurnWithSource(ctx, l.source, runner.WithContextBreakdown())
	return r, err
}
func (l *LiveRunner) buildHeadlessTurn(ctx context.Context, extraOpts ...options.Option[runner.Runner]) (*runner.Runner, *spawn.Group, error) {
	r, _, group, err := l.buildTurnWithSource(ctx, l.headlessSource, extraOpts...)
	return r, group, err
}
func (l *LiveRunner) buildTurnWithSource(ctx context.Context, sourceFn func(context.Context, string) (tools.Source, *tools.Registry, error), extraOpts ...options.Option[runner.Runner]) (*runner.Runner, bool, *spawn.Group, error) {
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
	engine, compactProv, compactModel := agentcompact.EngineTiered, prov, model
	if settings != nil {
		ctx := ctx
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
		sctx := ctx
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
	src, reg, err := sourceFn(ctx, searxngURL)
	if err != nil {
		return nil, false, nil, err
	}
	src = newOperationalSource(src, l.operational)
	evidence := newCompletionEvidence()
	group := spawn.NewGroup()
	coordinator := tools.NewWorkspaceCoordinator()
	src = coderunner.CoordinateWorkspace(src, coordinator)
	visible = NewModeFilteredSource(newEvidenceSource(src, evidence), l.isPlan)
	opts = append(opts, extraOpts...)
	opts = append(opts, runner.WithTurnQuality(NewAgentAwareTurnQuality(newPlanAwareTurnQuality(l.planStore, l.isPlan, evidence), group)))
	opts = append(opts, runner.WithTools(visible))
	r := runner.New(runner.ClientFromProvider(prov), opts...)
	// Late-register spawn onto the base registry now that the parent
	// runner exists (the registry enumerates lazily, so it's visible to
	// this turn). spawnDepth 0 leaves spawning disabled.
	l.registerSpawnTools(ctx, reg, r, group, coordinator, spawnDepth, spawnMaxIter)
	return r, thinking, group, nil
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
	engineName, compactProv, compactModel := agentcompact.EngineTiered, prov, model
	if settings != nil {
		engineName = settings.CompactEngine(ctx)
		compactProv, compactModel = settings.CompactorProvider(ctx, prov, model)
	}
	return l.conv.compactNow(ctx, buildLiveCompactor(engineName, window, compactProv, compactModel, l, l.ws.Root()), l.sink)
}

func (l *LiveRunner) RunTurn(ctx context.Context, prompt string) error {
	return l.RunTurnWithAttachments(ctx, prompt, nil)
}

func (l *LiveRunner) beginTurn(ctx context.Context) (context.Context, func(), error) {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		cancel()
		return nil, nil, errors.New("live runner is closing")
	}
	l.turnCancel = cancel
	l.turnDone = done
	l.mu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			close(done)
			l.mu.Lock()
			if l.turnDone == done {
				l.turnCancel = nil
				l.turnDone = nil
			}
			l.mu.Unlock()
		})
	}
	return runCtx, finish, nil
}

func (l *LiveRunner) RunTurnWithAttachments(ctx context.Context, prompt string, attachments []llm.ContentPart) error {
	return l.conv.transition(runner.TaskSpec{Prompt: prompt, Attachments: attachments}, func() (func(runner.TaskSpec) runner.TaskResult, error) {
		runCtx, finish, err := l.beginTurn(ctx)
		if err != nil {
			return nil, err
		}
		r, thinking, group, err := l.buildTurnWithSource(runCtx, l.source, runner.WithContextBreakdown())
		if err != nil {
			finish()
			return nil, err
		}
		return func(spec runner.TaskSpec) runner.TaskResult {
			defer finish()
			spec.Thinking = thinking
			result := r.Run(runCtx, spec)
			if closeErr := group.Close(runCtx); closeErr != nil && result.Err == nil {
				result.Reason = runner.TerminalError
				result.Err = closeErr
			}
			return result
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
