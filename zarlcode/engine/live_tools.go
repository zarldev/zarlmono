package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/hooks"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	computertools "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
	"github.com/zarldev/zarlmono/zkit/options"
)

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
	deps.DecomposeIgnoredTools = append(deps.DecomposeIgnoredTools,
		spawn.ToolNameAgentSpawn,
		spawn.ToolNameAgentAwait,
		spawn.ToolNameAgentStatus,
		spawn.ToolNameAgentStop,
		spawn.ToolNameListAgentTasks,
	)
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
func (l *LiveRunner) source(ctx context.Context, webSearch tools.Tool) (tools.Source, *tools.Registry, error) {
	return l.sourceWithDeps(ctx, webSearch, l.guardrailDeps(ctx))
}

func (l *LiveRunner) headlessSource(ctx context.Context, webSearch tools.Tool) (tools.Source, *tools.Registry, error) {
	return l.sourceWithDeps(ctx, webSearch, l.headlessGuardrailDeps(ctx))
}

func (l *LiveRunner) sourceWithDeps(ctx context.Context, webSearch tools.Tool, deps guardrails.Deps) (tools.Source, *tools.Registry, error) {
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
	// external search backend), so register it here when one is configured.
	// Registered before GuardedSource so it runs under the same guardrail
	// chain as every other tool.
	if enableWeb && webSearch != nil {
		_ = reg.Register(webSearch)
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
		base = ComposeSources(base, l.mcpHost)
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
		// Program is synthetic and was added after the ordinary source was
		// guarded. Guard only its outer boundary with a fresh chain; direct tools
		// keep using the original chain, while nested program calls re-enter it.
		programBoundary, err := sourcechain.New(programSource, deps)
		if err != nil {
			return nil, nil, fmt.Errorf("program boundary guardrails: %w", err)
		}
		guarded = RouteToolBoundary(programSource, programBoundary.Source, programtools.ToolName)
	}
	return guarded, reg, nil
}

func (l *LiveRunner) buildHeadlessTurn(ctx context.Context, extraOpts ...options.Option[runner.Runner]) (*runner.Runner, *turnResources, error) {
	r, _, resources, err := l.buildTurnWithSource(ctx, l.headlessSource, extraOpts...)
	return r, resources, err
}

type turnResources struct {
	group       *spawn.Group
	coordinator *tools.WorkspaceCoordinator
}

func (r *turnResources) Close(ctx context.Context) error {
	r.coordinator.BeginShutdown()
	return r.group.Close(ctx)
}

func (l *LiveRunner) buildTurnWithSource(ctx context.Context, sourceFn func(context.Context, tools.Tool) (tools.Source, *tools.Registry, error), extraOpts ...options.Option[runner.Runner]) (*runner.Runner, bool, *turnResources, error) {
	// Snapshot the (re-pointable) run target for this turn. The PLAN flag
	// is still read live by prompt/source closures so a mid-turn toggle
	// gates the next dispatch.
	l.mu.Lock()
	tgt := l.target
	settings := l.settings
	thinking := l.thinkingEnabledForLocked(tgt)
	l.mu.Unlock()
	prov, model, window, webSearch := tgt.Provider, tgt.Model, tgt.Window, tgt.WebSearch
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
	if l.toolOutputSink != nil {
		opts = append(opts, runner.WithToolOutputSink(l.toolOutputSink))
	}

	// Wrap the guarded source with the PLAN-mode filter, reading the flag
	// live so toggling mid-run gates the next dispatch.
	src, reg, err := sourceFn(ctx, webSearch)
	if err != nil {
		return nil, false, nil, err
	}
	src = newOperationalSource(src, l.operational)
	evidence := NewCompletionEvidence()
	spawnConcurrent := 0
	spawnRuntime := time.Duration(0)
	if l.settings != nil {
		spawnConcurrent = l.settings.SpawnMaxConcurrent(ctx)
		spawnRuntime = l.settings.SpawnMaxRuntime(ctx)
	}
	group := spawn.NewGroup(spawn.WithMaxConcurrent(spawnConcurrent), spawn.WithMaxRuntime(spawnRuntime))
	coordinator := tools.NewWorkspaceCoordinator()
	src = coderunner.CoordinateWorkspace(src, coordinator)
	visible = NewModeFilteredSource(WithCompletionEvidence(src, evidence), l.isPlan)
	opts = append(opts, extraOpts...)
	opts = append(opts, runner.WithTurnQuality(NewAgentAwareTurnQuality(newPlanAwareTurnQuality(l.planStore, l.isPlan, evidence), group)))
	opts = append(opts, runner.WithTools(visible))
	r := runner.New(runner.ClientFromProvider(prov), opts...)
	// Late-register spawn onto the base registry now that the parent
	// runner exists (the registry enumerates lazily, so it's visible to
	// this turn). spawnDepth 0 leaves spawning disabled.
	l.registerSpawnTools(ctx, reg, r, group, coordinator, spawnDepth, spawnMaxIter)
	return r, thinking, &turnResources{group: group, coordinator: coordinator}, nil
}
