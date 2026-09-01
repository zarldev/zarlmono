// Package spawn provides the spawn-agent tool: a registry-compatible
// tool that lets the running agent kick off a focused sub-task in a
// fresh runner.Run, returning only its summary as a single tool result.
//
// Lives in its own package (rather than inside zkit/agent/runner)
// because nothing in the runner's loop needs spawn — it's a tool the
// runner happens to expose if the consumer registers one. Consumers
// that don't want sub-agents
// simply don't register the tool.
//
// Recursion ceiling is owned by the tool instance, not the runner —
// each consumer chooses how many levels deep their agent can go.
package spawn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

const agentField = "agent"

// ToolNameAgentSpawn is the registered asynchronous agent launch name.
const ToolNameAgentSpawn tools.ToolName = "agent_spawn"

// defaultMaxDepth is the recursion ceiling applied when WithMaxDepth
// isn't supplied. One level — the parent task can delegate to a
// sub-agent, but that sub-agent cannot recursively spawn another.
//
// Larger cloud models (gpt-5.5/high observed in SWE-bench traces)
// interpret agent_spawn as a free-form fan-out primitive and burn
// out their context plus the provider's rate limit building deep
// trees of children whose results never converge back to a
// coherent plan. Capping the depth at 1 keeps agent_spawn a single
// delegation hop — the "researcher" / "code-reviewer" pattern —
// and forces the parent to flatten any deeper work into its own
// iteration loop, where the runner's guardrails and improvement
// loop actually fire.
//
// Consumers with a legitimate need for deeper nesting can pass
// [WithMaxDepth] explicitly; nothing in the runner architecturally
// prevents it, the cap just protects the common case from runaway
// recursion.
const defaultMaxDepth = 1

// plannerProbeTimeout caps the one-time ProbingPlanner.Probe health
// check fired on the first spawn. Short — a probe is meant to be a
// cheap liveness ping, not real work; if it can't answer in this
// window the planner is unhealthy enough to warn about anyway.
const plannerProbeTimeout = 5 * time.Second

// Tool is the spawn-agent tool. Construct with New, passing the
// runner the child task should execute on (typically the same runner
// the tool is registered against, so the child inherits provider,
// tools, sink, prompt source, etc.).
//
// Optional WithAgentResolver lets the consumer route children to
// different runners by name — e.g. zarlcode/tui exposes one
// runner per authored agent profile (with that agent's provider,
// model, prompt) so the parent can delegate "review this code" to
// a code_reviewer agent backed by Claude even when the parent
// itself is on Codex. Without a resolver, every child uses parent.
//
// Optional WithSpawnPlanner enables grammar-constrained recovery
// when the model omits or misspells the agent arg — the planner
// picks from the closed set of registered names. See SpawnPlanner.
//
// Optional WithSpawnMaxIterations sets a ceiling on child iterations.
// When set (>0), the tool clamps the child's MaxIterations to this
// value. When unset (0), the child inherits the parent runner's
// configured default.
type Tool struct {
	parent         *runner.Runner
	maxDepth       int
	spawnMaxIter   int
	resolveAgent   AgentResolver
	planner        SpawnPlanner
	plannerAgents  []AgentCandidate
	probeOnce      sync.Once
	modePolicy     func(SpawnMode, tools.ToolSpec) bool
	defaultAgents  map[SpawnMode]string
	modeMaxIter    map[SpawnMode]int
	defaultTargets map[SpawnMode]*runner.Runner
	fallback       FallbackPolicy
}

// AgentResolver returns the runner to use for a named sub-agent.
// Empty name means "use the parent runner" — implementations should
// handle that case as (nil, nil) so the tool falls back. An error
// is propagated to the model as a tool-result failure (e.g. agent
// not found, provider construction failed).
type AgentResolver func(name string) (*runner.Runner, error)

// AgentCandidate is one named sub-agent the spawn planner may choose.
// Description and Mode are optional hints used only for routing; Name is the
// closed-set value returned in SpawnPlan.Agent.
type AgentCandidate struct {
	Name        string
	Description string
	Mode        SpawnMode
}

func (c AgentCandidate) normalized() AgentCandidate {
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	mode := SpawnMode(strings.ToLower(strings.TrimSpace(string(c.Mode))))
	if mode.Valid() {
		c.Mode = mode
	} else {
		c.Mode = ""
	}
	return c
}

// New returns a spawn-agent tool wired to parent. Apply WithMaxDepth
// to set the recursion ceiling (default 1 — see [defaultMaxDepth]
// for the rationale); apply WithAgentResolver to enable named
// sub-agent dispatch.
func New(parent *runner.Runner, opts ...options.Option[Tool]) *Tool {
	t := &Tool{
		parent:   parent,
		maxDepth: defaultMaxDepth,
		fallback: FallbackPlanner,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// WithMaxDepth sets the recursion ceiling — at depth=N the tool
// refuses to spawn a child rather than running it. A value of 0
// disables spawning entirely (the tool always refuses). Negative
// values are ignored.
func WithMaxDepth(n int) options.Option[Tool] {
	return func(t *Tool) {
		if n >= 0 {
			t.maxDepth = n
		}
	}
}

// WithSpawnMaxIterations sets the host-controlled child iteration budget. When
// configured, this value is used for every child regardless of a model-supplied
// max_iterations value, so the model cannot accidentally shorten sub-agent work.
// Zero (the default) leaves iteration selection to the runner/tool argument.
func WithSpawnMaxIterations(n int) options.Option[Tool] {
	return func(t *Tool) {
		if n >= 0 {
			t.spawnMaxIter = n
		}
	}
}

// WithAgentResolver enables named sub-agent dispatch. When the model
// passes `agent="<name>"`, the tool calls resolve(name) to obtain
// the runner the child should execute on. Without a resolver, the
// agent argument is rejected with a clear "no named agents
// configured" error so the model knows to omit it.
func WithAgentResolver(resolve AgentResolver) options.Option[Tool] {
	return func(t *Tool) {
		t.resolveAgent = resolve
	}
}

// WithDefaultAgent selects a named agent profile when agent_spawn omits agent
// for the given work mode. Explicit agent arguments always win. Empty names and
// invalid modes are ignored; without a configured default the planner/parent
// fallback remains unchanged.
func WithDefaultAgent(mode SpawnMode, name string) options.Option[Tool] {
	return func(t *Tool) {
		mode = normalizeMode(string(mode))
		name = strings.TrimSpace(name)
		if !mode.Valid() || name == "" {
			return
		}
		if t.defaultAgents == nil {
			t.defaultAgents = make(map[SpawnMode]string)
		}
		t.defaultAgents[mode] = name
	}
}

func (t *Tool) applyDefaultAgent(args *Args) {
	if args == nil || strings.TrimSpace(args.Agent) != "" {
		return
	}
	mode := normalizeMode(args.Mode)
	if !mode.Valid() {
		mode = SpawnModeImplement
	}
	args.Agent = t.defaultAgents[mode]
}

// WithDefaultTarget selects a runner for unnamed tasks in one work mode. A
// named agent, whether explicit or host-defaulted, always takes precedence.
func WithDefaultTarget(mode SpawnMode, target *runner.Runner) options.Option[Tool] {
	return func(t *Tool) {
		mode = normalizeMode(string(mode))
		if !mode.Valid() || target == nil {
			return
		}
		if t.defaultTargets == nil {
			t.defaultTargets = make(map[SpawnMode]*runner.Runner)
		}
		t.defaultTargets[mode] = target
	}
}

// WithModeMaxIterations sets a host-controlled iteration budget for one mode.
// A positive mode budget overrides the shared spawn budget for that mode.
func WithModeMaxIterations(mode SpawnMode, n int) options.Option[Tool] {
	return func(t *Tool) {
		mode = normalizeMode(string(mode))
		if !mode.Valid() || n <= 0 {
			return
		}
		if t.modeMaxIter == nil {
			t.modeMaxIter = make(map[SpawnMode]int)
		}
		t.modeMaxIter[mode] = n
	}
}

// FallbackPolicy controls routing when no named agent resolves cleanly.
type FallbackPolicy string

const (
	// FallbackPlanner lets the planner recover missing/unknown names, then uses parent.
	FallbackPlanner FallbackPolicy = "planner"
	// FallbackParent skips planner recovery and immediately uses the parent runner.
	FallbackParent FallbackPolicy = "parent"
	// FallbackError refuses unresolved routing instead of silently using the parent.
	FallbackError FallbackPolicy = "error"
)

// WithFallbackPolicy configures unresolved named-agent routing.
func WithFallbackPolicy(policy FallbackPolicy) options.Option[Tool] {
	return func(t *Tool) {
		switch policy {
		case FallbackPlanner, FallbackParent, FallbackError:
			t.fallback = policy
		}
	}
}

// WithModeToolPolicy turns SpawnMode from advisory prompt text into
// enforced tool policy. policy reports whether a tool spec is allowed for
// a given mode; the spawn tool binds it to the child's mode and plants it
// on the child Run's ctx via runner.WithToolGate, so the runner hides the
// disallowed tools from the child and refuses them if called anyway.
//
// The policy can filter by capability (e.g., spec.Mutates) rather than
// enumerating tool names. Tools like bash that can read or write should be
// handled specially since they typically leave Mutates=false. Without this
// option, mode is recorded and prompted but not enforced. An empty/unknown
// mode is never gated, so a plain spawn (no mode) keeps the full tool surface.
func WithModeToolPolicy(policy func(SpawnMode, tools.ToolSpec) bool) options.Option[Tool] {
	return func(t *Tool) { t.modePolicy = policy }
}

// SpawnMode is the closed set of work modes a planner can assign to
// a sub-agent. The mode is prepended to the child's prompt so the
// child sees its scope explicitly. Kept short and discriminative —
// these aren't job titles, they're the orthogonal axes that change
// how the child should approach its prompt.
type SpawnMode string

const (
	// SpawnModeExplore is read-only investigation: file reads, greps,
	// build queries. The child should NOT mutate files.
	SpawnModeExplore SpawnMode = "explore"

	// SpawnModeImplement is the make-changes mode: file writes, edits,
	// code-mutating tools are in play.
	SpawnModeImplement SpawnMode = "implement"

	// SpawnModeVerify is review / sanity-check: run tests, lint,
	// re-read changes. Output is a verdict, not a change set.
	SpawnModeVerify SpawnMode = "verify"
)

// Valid reports whether m is one of the three known modes. Used to
// reject malformed planner output before it shapes a dispatch.
func (m SpawnMode) Valid() bool {
	switch m {
	case SpawnModeExplore, SpawnModeImplement, SpawnModeVerify:
		return true
	}
	return false
}

// SpawnPlan is what a SpawnPlanner returns. Rationale is the model's
// chain-of-thought, captured ahead of the constrained enums per the
// established "rationale first" schema pattern (see
// zkit/agent/guardrails/decompose_judge.go). Agent is one of the
// names supplied to WithSpawnPlanner — or "" to use the parent
// runner. Mode shapes the prompt that lands on the child.
type SpawnPlan struct {
	Rationale string
	Agent     string
	Mode      SpawnMode
}

// SpawnPlanInput is the context a planner sees. Prompt is the task
// the model wanted to delegate; AvailableAgents is the closed set of
// names the planner must choose from (or leave empty for parent).
type SpawnPlanInput struct {
	Prompt          string
	AvailableAgents []AgentCandidate
}

// SpawnPlanner is the optional hook the spawn tool consults when the
// model omits the agent arg OR supplies a name not in the registered
// set. The planner picks an agent (from the closed set) plus a mode,
// using grammar-constrained sampling so it cannot invent a name that
// the AgentResolver doesn't recognise — which is exactly the
// confabulation surface this work targets (see
// feedback_enum_schemas_beat_instructions).
//
// When the model picks a recognised agent name explicitly, the
// planner is NOT consulted — the model's pick wins. The planner
// only fires on the soft-fallback path that used to silently route
// to the parent runner and lose the routing signal entirely.
//
// Implementations must be safe for concurrent use; multiple Execute
// calls (parallel fan-out) can land here at the same time.
type SpawnPlanner interface {
	Plan(ctx context.Context, in SpawnPlanInput) (SpawnPlan, error)
}

// ProbingPlanner is the optional extension a SpawnPlanner implements
// when it supports a cheap health-check. The spawn tool calls Probe
// exactly once, on the first applyPlanner invocation, before any
// early return — so a broken planner (provider down, grammar
// misconfigured) is surfaced in the logs even when the model always
// picks a valid agent name and the planner's Plan path never runs.
// Probe should be fast and side-effect-free; a non-nil error is logged
// at warn, not fatal — the planner still soft-falls-back per call.
type ProbingPlanner interface {
	SpawnPlanner
	Probe(ctx context.Context) error
}

// WithSpawnPlanner wires a planner the tool consults when the model
// omits the `agent` arg or supplies a name that's not in agents. The names
// slice is the closed enum the planner is constrained to and must match the
// set the wired AgentResolver recognises. Use WithSpawnPlannerCandidates when
// descriptions or profile default modes are available; this compatibility
// helper keeps older name-only callers working.
//
// A nil planner or empty names slice is a no-op: the tool preserves
// today's soft-fallback-to-parent behavior. Both must be supplied
// for the planner to fire.
func WithSpawnPlanner(planner SpawnPlanner, agents []string) options.Option[Tool] {
	return func(t *Tool) {
		t.planner = planner
		candidates := make([]AgentCandidate, 0, len(agents))
		for _, name := range agents {
			candidates = append(candidates, AgentCandidate{Name: name})
		}
		t.plannerAgents = normalizeAgentCandidates(candidates)
	}
}

// WithSpawnPlannerCandidates wires a planner with the full agent catalogue the
// router should consider. Empty names are ignored. Invalid or empty modes mean
// the candidate has no profile-mode default.
func WithSpawnPlannerCandidates(planner SpawnPlanner, agents []AgentCandidate) options.Option[Tool] {
	return func(t *Tool) {
		t.planner = planner
		t.plannerAgents = normalizeAgentCandidates(agents)
	}
}

func normalizeAgentCandidates(agents []AgentCandidate) []AgentCandidate {
	out := make([]AgentCandidate, 0, len(agents))
	seen := map[string]bool{}
	for _, agent := range agents {
		agent = agent.normalized()
		if agent.Name == "" || seen[agent.Name] {
			continue
		}
		seen[agent.Name] = true
		out = append(out, agent)
	}
	return out
}

func agentCandidateNames(agents []AgentCandidate) []string {
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		if agent.Name != "" {
			names = append(names, agent.Name)
		}
	}
	return names
}

func findAgentCandidate(agents []AgentCandidate, name string) (AgentCandidate, bool) {
	name = strings.TrimSpace(name)
	for _, agent := range agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return AgentCandidate{}, false
}

// Args is the typed argument struct Tool.Execute decodes into via
// tools.DecodeArgs. Field tags match the JSON Schema in Definition.
type Args struct {
	Prompt        string `json:"prompt" doc:"The task for the sub-agent. Be specific — the sub-agent has none of your context."`
	Agent         string `json:"agent,omitempty" doc:"Optional named agent to dispatch to (must be one returned by list_agents). Empty/omitted = use the parent's provider/model/prompt."`
	Mode          string `json:"mode,omitempty" doc:"Optional work mode: 'explore' (read-only investigation), 'verify' (tests/builds without file edits), or 'implement' (full tool surface, the default). When the host enforces work-mode policies, disallowed tools are blocked literally."`
	MaxIterations int    `json:"max_iterations,omitempty" doc:"Optional non-negative iteration cap. Prefer omitting (0) — the host applies the configured sub-agent limit automatically."`
}

// Definition advertises agent_spawn: prompt is required; agent, mode, and
// max_iterations are optional. The long description carries the usage
// contract the schema can't — parallel fan-out, the recursion cap, and
// named-agent dispatch via the agent arg.
func (*Tool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameAgentSpawn,
		Description: "Run a focused sub-task in a fresh agent. The sub-agent has the same tools but no memory of your conversation — describe the task fully. This compatibility implementation waits for the summary; production registration uses the asynchronous agent_spawn protocol. Pass an `agent` name returned by list_agents or omit it to use the parent runner.",
		Parameters:  tools.SchemaFor[Args](),
	}
}

// Execute refuses past the recursion ceiling, consults the optional planner
// for a missing/unknown agent name, resolves the target runner (soft
// fallback to the parent with a notice), then runs the child at depth+1
// with the work mode and mode tool-gate planted on the child ctx. Only the
// shaped summary (notices + final content + iterations/reason) is returned.
func (t *Tool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, derr := tools.DecodeArgs[Args](call.Arguments)
	if derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	depth := taskscope.DepthFrom(ctx)
	if depth >= t.maxDepth {
		return tools.Failure(call.ID, tools.Budget("agent_spawn",
			fmt.Sprintf("max recursion depth %d reached — flatten the work or stop calling tools", t.maxDepth))), nil
	}
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return tools.Failure(call.ID, tools.Validation("agent_spawn", "prompt is required")), nil
	}
	if args.MaxIterations < 0 {
		return tools.Failure(call.ID, tools.Validation("agent_spawn", "max_iterations must be non-negative")), nil
	}
	explicitMode := argsModeExplicit(call.Arguments)
	if explicitMode && strings.TrimSpace(args.Mode) != "" && !normalizeMode(args.Mode).Valid() {
		return tools.Failure(call.ID, tools.Validation("agent_spawn",
			fmt.Sprintf("mode %q is invalid; use explore, verify, or implement", args.Mode))), nil
	}
	t.applyDefaultAgent(&args)

	// Optional planner rescue: when the agent omitted the name or
	// picked one not in the registered set, ask a grammar-constrained
	// planner to pick from the closed set. On success the planner's
	// rationale is appended to the child's summary so the parent sees
	// what was decided and why. On any failure (no planner wired,
	// planner errored, returned invalid output) the original args
	// flow through unchanged — today's soft-fallback path catches it
	// later.
	plannerNote := ""
	if t.fallback == FallbackPlanner {
		plannerNote = t.applyPlanner(ctx, &args)
	}
	if err := t.strictRoutingError(args); err != nil {
		return tools.Failure(call.ID, err), nil
	}

	// Pick the runner the child should execute on (parent, or a named
	// agent via the resolver — see resolveTarget for the soft-fallback).
	profileMode := SpawnMode("")
	if candidate, ok := findAgentCandidate(t.plannerAgents, args.Agent); ok {
		profileMode = candidate.Mode
	}
	target, agentLoaded, fallbackNotice := t.resolveTarget(args)
	if t.fallback == FallbackError && !agentLoaded && strings.TrimSpace(args.Agent) != "" {
		return tools.Failure(call.ID, tools.Validation("agent_spawn", fmt.Sprintf("agent %q could not be loaded and fallback=error", strings.TrimSpace(args.Agent)))), nil
	}
	if target == nil {
		return tools.Failure(call.ID, tools.Fatal("agent_spawn", errors.New("parent runner is nil"))), nil
	}

	mode := t.effectiveMode(args, profileMode, explicitMode)
	childSpec := runner.TaskSpec{
		ID:               taskscope.ID(uuid.NewString()),
		Prompt:           childPromptWithMode(args.Prompt, mode),
		MaxIterations:    t.spawnMaxIterations(mode, args.MaxIterations),
		Depth:            depth + 1,
		ParentToolCallID: call.ID.String(),
	}
	if agentLoaded {
		childSpec.AgentName = args.Agent
	}

	// Enforce the child's work mode as policy on the child Run's ctx.
	// Two layers: the mode itself is planted (via taskscope) so per-call
	// policy can act on it — the shell guardrail's verify profile blocks
	// workspace-mutating bash in verify mode — and, when a mode policy
	// is wired, a tool gate hides and refuses the tools the mode
	// disallows. With no valid mode the child keeps the full surface.
	runCtx := ctx
	if mode != "" {
		if wm, err := taskscope.ParseWorkMode(string(mode)); err == nil {
			runCtx = taskscope.WithWorkMode(runCtx, wm)
		}
		if t.modePolicy != nil {
			runCtx = runner.WithToolGate(runCtx, func(spec tools.ToolSpec) bool {
				return t.modePolicy(mode, spec)
			})
		}
	}

	res := target.Run(runCtx, childSpec)
	return shapeResult(call, res, args.Agent, agentLoaded, plannerNote, fallbackNotice), nil
}

// resolveTarget uses a mode-specific default runner for unnamed tasks when
// configured, otherwise the parent. Named agents use the consumer resolver;
// missing or unknown names soft-fall back to the parent with a notice.
func (t *Tool) resolveTarget(args Args) (*runner.Runner, bool, string) {
	if args.Agent == "" {
		mode := normalizeMode(args.Mode)
		if !mode.Valid() {
			mode = SpawnModeImplement
		}
		if target := t.defaultTargets[mode]; target != nil {
			return target, false, ""
		}
		return t.parent, false, ""
	}
	if t.resolveAgent == nil {
		return t.parent, false, fmt.Sprintf(
			"note: no named agents are configured in this workspace, so the request for agent=%q "+
				"ran on the default runner. Call list_agents to see available profiles, then pick one of those or omit the `agent` arg.",
			args.Agent)
	}
	r, err := t.resolveAgent(args.Agent)
	if err != nil || r == nil {
		return t.parent, false, fmt.Sprintf(
			"note: agent=%q is not registered (%v), so the request ran on the default runner. "+
				"Call list_agents to see available profiles, then pick one of those, or omit the `agent` arg "+
				"to suppress this notice.",
			args.Agent, err)
	}
	return r, true, ""
}

func (t *Tool) strictRoutingError(args Args) *tools.Error {
	if t.fallback != FallbackError {
		return nil
	}
	name := strings.TrimSpace(args.Agent)
	if name != "" {
		if len(t.plannerAgents) > 0 {
			if _, registered := findAgentCandidate(t.plannerAgents, name); !registered {
				return tools.Validation("agent_spawn", fmt.Sprintf("agent %q is not registered and fallback=error", name))
			}
		}
		return nil
	}
	mode := normalizeMode(args.Mode)
	if !mode.Valid() {
		mode = SpawnModeImplement
	}
	if t.defaultTargets[mode] == nil {
		return tools.Validation("agent_spawn", "no default agent or target is configured for this mode and fallback=error")
	}
	return nil
}

// shapeResult builds the tool result from the child's terminal TaskResult.
// A failed child encodes its failure in res (Reason != completed); notices
// (planner rationale, agent fallback — in display order, empties skipped)
// are prepended to the summary. A non-completed child becomes a BUDGET-kind
// failure that still carries the summary in Error, because the runner renders
// failed tool results from Error only — otherwise a sub-agent that produced a
// useful wrap-up before hitting its budget would look like an opaque failure
// and the summary would be dropped on the floor.
func shapeResult(call tools.ToolCall, res runner.TaskResult, agentName string, agentLoaded bool, notices ...string) *tools.ToolResult {
	summary := strings.TrimSpace(res.FinalContent)
	if summary == "" {
		summary = fmt.Sprintf("(sub-agent ended with reason=%s, no final content)", res.Reason)
	}
	parts := make([]string, 0, len(notices)+1)
	for _, n := range notices {
		if n != "" {
			parts = append(parts, n)
		}
	}
	summary = strings.Join(append(parts, summary), "\n\n")

	success := res.Reason == runner.TerminalCompleted
	result := &tools.ToolResult{
		ToolCallID: call.ID,
		Success:    success,
		Data: map[string]any{
			"summary":      summary,
			"iterations":   res.Iterations,
			"reason":       string(res.Reason),
			agentField:     agentName,
			"agent_loaded": agentLoaded,
		},
		ExecutedAt: time.Now(),
	}
	if !success {
		result.Err = terminalToolError("agent_spawn", res.Reason, res.Iterations, summary, errorString(res.Err), false)
		result.Error = result.Err.Error()
	}
	return result
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

const childSummaryContract = `

Sub-agent completion contract:
- Your job is to return a concise final summary to the parent agent. The parent only sees your final answer, not your full transcript.
- Prefer a useful partial summary over another tool call when budget or time is tight.
- Before the iteration cap or timeout, stop using tools and answer in plain text with: what you found, what you changed (if anything), blockers/uncertainties, and recommended next steps.
- If you cannot complete the task, still produce a final summary of the evidence gathered so far and why you stopped.`

func childPrompt(prompt string) string {
	return strings.TrimSpace(prompt) + childSummaryContract
}

func childPromptWithMode(prompt string, mode SpawnMode) string {
	if mode.Valid() {
		prompt = fmt.Sprintf("[mode: %s] %s", mode, prompt)
	}
	return childPrompt(prompt)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// spawnMaxIterations resolves the effective max iterations for a child task.
// A configured host budget always wins: treating a model-specified value as a
// lower cap lets the model accidentally cut focused sub-agent work short (for
// example, repeatedly choosing 5 despite a configured budget of 20). Without a
// configured budget, preserve the model value; zero lets the runner use its
// own default.
func (t *Tool) spawnMaxIterations(mode SpawnMode, modelSpec int) int {
	if n := t.modeMaxIter[mode]; n > 0 {
		return n
	}
	if t.spawnMaxIter > 0 {
		return t.spawnMaxIter
	}
	return modelSpec
}

// applyPlanner consults the wired planner when the model's agent
// pick would otherwise soft-fall-back to parent — i.e. the arg is
// empty, or the name isn't in the registered set. On a clean verdict
// the planner's choice mutates args (agent name + mode-prefixed
// prompt) and returns a one-line note describing what changed so
// the parent agent sees the rerouting. On any failure (no planner
// wired, gated out, planner errored, invalid verdict) it returns ""
// and leaves args alone — the existing soft-fallback path takes
// over downstream.
func (t *Tool) applyPlanner(ctx context.Context, args *Args) string {
	// Probe the planner once, before any early return below. The
	// model-picked-a-valid-name short-circuit means a healthy run can
	// reach the planner's Plan path zero times, so without a probe a
	// broken planner stays silent until the first bad pick — which may
	// never come. Firing here, on the first spawn of the run, surfaces
	// misconfiguration at warn while keeping New a pure constructor (the
	// probe needs a real ctx, which only Execute has).
	t.probeOnce.Do(func() {
		pp, ok := t.planner.(ProbingPlanner)
		if !ok {
			return
		}
		probeCtx, cancel := context.WithTimeout(ctx, plannerProbeTimeout)
		defer cancel()
		if err := pp.Probe(probeCtx); err != nil {
			slog.WarnContext(ctx, "spawn: planner probe failed; soft-fallbacks will be silent until the first bad agent pick", "err", err)
		}
	})

	if t.planner == nil || len(t.plannerAgents) == 0 {
		return ""
	}
	// Only fire when the agent's pick wouldn't resolve cleanly today.
	// A correctly-spelled, registered name short-circuits — the
	// model already made a good call and the planner adds nothing.
	if args.Agent != "" && findAgentCandidateName(t.plannerAgents, args.Agent) {
		return ""
	}

	plan, err := t.planner.Plan(ctx, SpawnPlanInput{
		Prompt:          args.Prompt,
		AvailableAgents: t.plannerAgents,
	})
	if err != nil {
		// Log so a misconfigured planner (e.g. a provider that doesn't
		// honour the grammar constraint) is detectable — otherwise the
		// soft-fallback hides it and the operator sees only parent runs.
		slog.WarnContext(ctx, "spawn: planner failed, falling back to parent", "err", err)
		return ""
	}
	// The planner is constrained to pick from plannerAgents (or "")
	// and one of three modes — but defend against malformed output
	// in case the planner's provider doesn't honour the grammar.
	// Either issue means we silently fall back rather than emit a
	// half-baked plan into the child's prompt.
	if plan.Agent != "" && !findAgentCandidateName(t.plannerAgents, plan.Agent) {
		slog.WarnContext(ctx, "spawn: planner returned an unregistered agent, falling back to parent", "agent", plan.Agent)
		return ""
	}
	if !plan.Mode.Valid() {
		slog.WarnContext(ctx, "spawn: planner returned an invalid mode, falling back to parent", "mode", plan.Mode)
		return ""
	}

	args.Agent = plan.Agent
	args.Mode = string(plan.Mode)

	target := plan.Agent
	if target == "" {
		target = "parent"
	}
	return fmt.Sprintf(
		"note: planner routed this delegation to agent=%q in mode=%s. Rationale: %s",
		target, plan.Mode, plan.Rationale)
}

func (t *Tool) effectiveMode(args Args, profileMode SpawnMode, _ bool) SpawnMode {
	argMode := normalizeMode(args.Mode)
	profileMode = normalizeMode(string(profileMode))
	if profileMode.Valid() && argMode.Valid() {
		return stricterMode(profileMode, argMode)
	}
	if profileMode.Valid() {
		return profileMode
	}
	if argMode.Valid() {
		return argMode
	}
	return SpawnModeImplement
}

func normalizeMode(raw string) SpawnMode {
	mode := SpawnMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode.Valid() {
		return mode
	}
	return ""
}

func stricterMode(a, b SpawnMode) SpawnMode {
	if modeRank(a) <= modeRank(b) {
		return a
	}
	return b
}

func modeRank(mode SpawnMode) int {
	switch mode {
	case SpawnModeExplore:
		return 0
	case SpawnModeVerify:
		return 1
	case SpawnModeImplement:
		return 2
	default:
		return 3
	}
}

func argsModeExplicit(params tools.ToolParameters) bool {
	_, ok := params["mode"]
	return ok
}

func findAgentCandidateName(agents []AgentCandidate, name string) bool {
	_, ok := findAgentCandidate(agents, name)
	return ok
}
