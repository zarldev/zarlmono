// Package spawn provides the spawn-agent tool: a registry-compatible
// tool that lets the running agent kick off a focused sub-task in a
// fresh runner.Run, returning only its summary as a single tool result.
//
// Lives in its own package (rather than inside zkit/agent/runner)
// because nothing in the runner's loop needs spawn — it's a tool the
// runner happens to expose if the consumer registers one. Consumers
// that don't want sub-agents (zarlai's stateless one-shot endpoints,
// say) simply don't register the tool.
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

// ToolNameSpawnAgent is the registered name of the spawn-agent tool.
const ToolNameSpawnAgent tools.ToolName = "spawn_agent"

// defaultMaxDepth is the recursion ceiling applied when WithMaxDepth
// isn't supplied. One level — the parent task can delegate to a
// sub-agent, but that sub-agent cannot recursively spawn another.
//
// Larger cloud models (gpt-5.5/high observed in SWE-bench traces)
// interpret spawn_agent as a free-form fan-out primitive and burn
// out their context plus the provider's rate limit building deep
// trees of children whose results never converge back to a
// coherent plan. Capping the depth at 1 keeps spawn_agent a single
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
	parent        *runner.Runner
	maxDepth      int
	spawnMaxIter  int
	resolveAgent  AgentResolver
	planner       SpawnPlanner
	plannerAgents []AgentCandidate
	probeOnce     sync.Once
	modePolicy    func(SpawnMode, tools.ToolSpec) bool
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

// WithSpawnMaxIterations sets a ceiling on child iterations. When the
// model doesn't specify max_iterations (or sets it to 0), the tool
// uses this value. When the model specifies a positive value, it's
// clamped to this ceiling — the model cannot exceed the configured
// sub-agent budget. Zero (the default) means "inherit from the runner".
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
	Mode          string `json:"mode,omitempty" doc:"Optional work mode: 'explore' (read-only investigation — the host blocks file edits and shell), 'verify' (run tests/builds, no file edits), or 'implement' (full tool surface, the default). When the host enforces it, an explore sub-agent literally cannot write or edit files."`
	MaxIterations int    `json:"max_iterations,omitempty" doc:"Optional iteration cap. Prefer omitting (0) — the host applies the configured sub-agent limit automatically."`
}

// Definition advertises spawn_agent: prompt is required; agent, mode, and
// max_iterations are optional. The long description carries the usage
// contract the schema can't — parallel fan-out, the recursion cap, and
// named-agent dispatch via the agent arg.
func (*Tool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolNameSpawnAgent,
		Description: "Run a focused sub-task in a fresh agent. The sub-agent has the same tools but no memory of your conversation — describe the task fully. Returns its final summary plus iteration count and termination reason. Use to keep your context clean during multi-step detours (research, refactors, builds). **Emit multiple spawn_agent calls in the same response to run sub-tasks in parallel** — the runner dispatches them concurrently. A small handful (researcher + reviewer + coder shape) is the intended use; if the host enforces a per-task fan-out cap you'll see a clear guardrail error before further calls run. Sub-agents cannot themselves spawn further sub-agents under the default recursion cap — flatten deeper work into your own iteration loop. Pass an `agent` name to delegate to a sub-agent backed by a different provider/model/prompt; call list_agents to discover available names. Omit the arg to run on the parent's provider/model.",
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
		return tools.Failure(call.ID, tools.Budget("spawn_agent",
			fmt.Sprintf("max recursion depth %d reached — flatten the work or stop calling tools", t.maxDepth))), nil
	}
	if args.Prompt == "" {
		return tools.Failure(call.ID, tools.Validation("spawn_agent", "prompt is required")), nil
	}

	// Optional planner rescue: when the agent omitted the name or
	// picked one not in the registered set, ask a grammar-constrained
	// planner to pick from the closed set. On success the planner's
	// rationale is appended to the child's summary so the parent sees
	// what was decided and why. On any failure (no planner wired,
	// planner errored, returned invalid output) the original args
	// flow through unchanged — today's soft-fallback path catches it
	// later.
	explicitMode := argsModeExplicit(call.Arguments)
	plannerNote := t.applyPlanner(ctx, &args)

	// Pick the runner the child should execute on (parent, or a named
	// agent via the resolver — see resolveTarget for the soft-fallback).
	profileMode := SpawnMode("")
	if candidate, ok := findAgentCandidate(t.plannerAgents, args.Agent); ok {
		profileMode = candidate.Mode
	}
	target, agentLoaded, fallbackNotice := t.resolveTarget(args)
	if target == nil {
		return tools.Failure(call.ID, tools.Fatal("spawn_agent", errors.New("parent runner is nil"))), nil
	}

	mode := t.effectiveMode(args, profileMode, explicitMode)
	childSpec := runner.TaskSpec{
		ID:               taskscope.ID(uuid.NewString()),
		Prompt:           childPromptWithMode(args.Prompt, mode),
		MaxIterations:    t.spawnMaxIterations(args.MaxIterations),
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

// resolveTarget picks the runner the child should execute on. An empty
// agent name (or no resolver wired) → the parent. A named agent → the
// consumer-side resolver; a missing resolver or an unknown agent name
// SOFT-FALLS-BACK to the parent with a one-line notice (empty on the happy
// path) for the summary. Hard-erroring used to send the model down a "no
// agents defined → I'll do this manually myself" detour where it read every
// file in the workspace one at a time — the spawn was the whole point.
func (t *Tool) resolveTarget(args Args) (*runner.Runner, bool, string) {
	if args.Agent == "" {
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
		result.Err = tools.Budget("spawn_agent", fmt.Sprintf("sub-agent ended with reason=%s after %d iteration%s. Summary:\n%s",
			res.Reason, res.Iterations, pluralS(res.Iterations), summary))
		result.Error = result.Err.Error()
	}
	return result
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
// When the tool has a configured ceiling (spawnMaxIter > 0):
//   - model-specified 0 or negative → use the ceiling
//   - model-specified positive → clamp to the ceiling
//
// When the tool has no ceiling (spawnMaxIter == 0):
//   - model-specified 0 → leave 0 (runner inherits its own default)
//   - model-specified positive → pass through (model chooses)
func (t *Tool) spawnMaxIterations(modelSpec int) int {
	if t.spawnMaxIter <= 0 {
		return modelSpec
	}
	if modelSpec <= 0 || modelSpec > t.spawnMaxIter {
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

func (t *Tool) effectiveMode(args Args, profileMode SpawnMode, explicit bool) SpawnMode {
	argMode := normalizeMode(args.Mode)
	profileMode = normalizeMode(string(profileMode))
	if explicit && argMode.Valid() {
		return argMode
	}
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
