package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	boardtools "github.com/zarldev/zarlmono/zkit/agent/tools/board"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// spawnTarget retains a runner and its registry for the turn's construction phase.
// A zero value denotes an unavailable optional mode target.
type spawnTarget struct {
	runner   *runner.Runner
	registry *tools.Registry
}

type spawnTargets struct {
	explore, verify, implement spawnTarget
}

func (l *LiveRunner) registerSpawnTools(ctx context.Context, reg *tools.Registry, parent *runner.Runner, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, maxDepth, spawnMaxIter int, scope inference.Scope, observed func(context.Context, AuxiliaryObservation), recording childRecordingSinkFactory, auxiliary auxiliaryProviderFactory, boardAccess ...boardtools.Board) {
	if maxDepth <= 0 {
		return
	}
	if l.settings != nil && !l.settings.SpawnEnabled(ctx) {
		return
	}
	modes := SpawnModesConfig{}
	if l.settings != nil {
		modes = l.settings.SpawnModes(ctx)
	}
	// Build the finite set before wiring any spawn tools. Recursively building
	// each default's defaults both resets the depth budget and expands a tree
	// before a single task can run. These runners are shared within this turn,
	// just as they are shared by sibling tasks; taskscope enforces actual depth.
	targets := spawnTargets{
		explore:   l.buildDefaultSpawnTarget(ctx, group, coordinator, modes.Explore, scope, observed, recording, auxiliary, boardAccess...),
		verify:    l.buildDefaultSpawnTarget(ctx, group, coordinator, modes.Verify, scope, observed, recording, auxiliary, boardAccess...),
		implement: l.buildDefaultSpawnTarget(ctx, group, coordinator, modes.Implement, scope, observed, recording, auxiliary, boardAccess...),
	}
	l.registerSpawnTargets(ctx, reg, parent, group, coordinator, maxDepth, spawnMaxIter, modes, targets, scope, observed, recording, auxiliary, boardAccess...)
	for _, target := range [...]spawnTarget{targets.explore, targets.verify, targets.implement} {
		if target.runner != nil {
			l.registerSpawnTargets(ctx, target.registry, target.runner, group, coordinator, maxDepth, spawnMaxIter, modes, targets, scope, observed, recording, auxiliary, boardAccess...)
		}
	}
}

func (l *LiveRunner) registerSpawnTargets(ctx context.Context, reg *tools.Registry, parent *runner.Runner, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, maxDepth, spawnMaxIter int, modes SpawnModesConfig, targets spawnTargets, scope inference.Scope, observed func(context.Context, AuxiliaryObservation), recording childRecordingSinkFactory, auxiliary auxiliaryProviderFactory, boardAccess ...boardtools.Board) {
	awaitTimeout := 30 * time.Second
	awaitMaxTimeout := 5 * time.Minute
	if l.settings != nil {
		if seconds := l.settings.Limits(ctx).SpawnAwaitTimeout; seconds > 0 {
			awaitTimeout = time.Duration(seconds) * time.Second
		}
		if seconds := l.settings.Limits(ctx).SpawnAwaitMaxTimeout; seconds > 0 {
			awaitMaxTimeout = time.Duration(seconds) * time.Second
		} else {
			awaitMaxTimeout = 0
		}
	}
	fallback := spawn.FallbackPlanner
	if l.settings != nil {
		fallback = spawn.FallbackPolicy(l.settings.SpawnFallback(ctx))
	}
	var planner spawn.SpawnPlanner
	var candidates []spawn.AgentCandidate
	var plannerTarget auxiliaryTarget
	if l != nil {
		l.mu.Lock()
		plannerTarget = auxiliaryTargetFromRun(l.target)
		l.mu.Unlock()
	}
	if l != nil && plannerTarget.provider != nil && l.catalog != nil {
		for _, agent := range l.catalog.Agents() {
			candidates = append(candidates, spawn.AgentCandidate{
				Name:        agent.Name,
				Description: agent.Description,
				Mode:        spawn.SpawnMode(agent.Mode),
			})
		}
		if len(candidates) > 0 {
			planner = spawn.NewLLMSpawnPlanner(auxiliary.bind(AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER, plannerTarget), spawn.WithPlannerInferenceScope(scope, plannerTarget.model),
				spawn.WithPlannerObservationSink(func(ctx context.Context, o spawn.PlanObservation) {
					observed(ctx, AuxiliaryObservation{Kind: AuxiliaryKinds.SPAWNPLANNER, Inference: o.Inference, ResponseValid: o.Valid})
				}))
		}
	}
	launch := spawn.NewAsync(parent, group,
		spawn.WithMaxDepth(maxDepth),
		spawn.WithAgentResolver(func(name string) (*runner.Runner, error) {
			return l.resolveAgentRunner(ctx, group, coordinator, name, maxDepth, spawnMaxIter, modes, targets, scope, observed, recording, auxiliary, boardAccess...)
		}),
		spawn.WithSpawnPlannerCandidates(planner, candidates),
		spawn.WithSpawnMaxIterations(spawnMaxIter),
		spawn.WithModeToolPolicy(coderunner.SpawnModePolicy()),
		spawn.WithDefaultAgent(spawn.SpawnModeExplore, modes.Explore.DefaultAgent),
		spawn.WithDefaultAgent(spawn.SpawnModeVerify, modes.Verify.DefaultAgent),
		spawn.WithDefaultAgent(spawn.SpawnModeImplement, modes.Implement.DefaultAgent),
		spawn.WithDefaultTarget(spawn.SpawnModeExplore, targets.explore.runner),
		spawn.WithDefaultTarget(spawn.SpawnModeVerify, targets.verify.runner),
		spawn.WithDefaultTarget(spawn.SpawnModeImplement, targets.implement.runner),
		spawn.WithModeMaxIterations(spawn.SpawnModeExplore, modes.Explore.MaxIterations),
		spawn.WithModeMaxIterations(spawn.SpawnModeVerify, modes.Verify.MaxIterations),
		spawn.WithModeMaxIterations(spawn.SpawnModeImplement, modes.Implement.MaxIterations),
		spawn.WithFallbackPolicy(fallback),
	)
	launch.Register(reg, spawn.WithAwaitTimeout(awaitTimeout), spawn.WithAwaitMaxTimeout(awaitMaxTimeout))
}

func (l *LiveRunner) buildDefaultSpawnTarget(ctx context.Context, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, mode SpawnModeConfig, scope inference.Scope, observed func(context.Context, AuxiliaryObservation), recording childRecordingSinkFactory, auxiliary auxiliaryProviderFactory, boardAccess ...boardtools.Board) spawnTarget {
	if mode.DefaultAgent != "" || mode.DefaultTarget.Name == "" && mode.DefaultTarget.Model == "" {
		return spawnTarget{}
	}
	target, err := l.buildAgentRunner(ctx, group, coordinator, catalog.Agent{
		Provider: mode.DefaultTarget.Name,
		Model:    mode.DefaultTarget.Model,
	}, scope, observed, recording, auxiliary, boardAccess...)
	if err != nil {
		slog.WarnContext(ctx, "spawn: default mode target unavailable", "provider", mode.DefaultTarget.Name, "model", mode.DefaultTarget.Model, "err", err)
		return spawnTarget{}
	}
	return target
}

func (l *LiveRunner) resolveAgentRunner(ctx context.Context, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, name string, maxDepth, spawnMaxIter int, modes SpawnModesConfig, targets spawnTargets, scope inference.Scope, observed func(context.Context, AuxiliaryObservation), recording childRecordingSinkFactory, auxiliary auxiliaryProviderFactory, boardAccess ...boardtools.Board) (*runner.Runner, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("empty agent name")
	}
	if l == nil || l.catalog == nil {
		return nil, errors.New("no agent catalog configured")
	}
	l.catalog.ReloadContext(ctx, l.ws.Root())
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	agent, ok := l.catalog.Agent(name)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", name)
	}
	target, err := l.buildAgentRunner(ctx, group, coordinator, agent, scope, observed, recording, auxiliary, boardAccess...)
	if err != nil {
		return nil, err
	}
	l.registerSpawnTargets(ctx, target.registry, target.runner, group, coordinator, maxDepth, spawnMaxIter, modes, targets, scope, observed, recording, auxiliary, boardAccess...)
	return target.runner, nil
}

// PrepareChildTarget resolves the actual construction for an agent profile.
// Unchanged children borrow the parent's provider and generation with independently
// cloned facts. Overrides create a fresh construction and resolve their own budget;
// unknown child limits use the application fallback, never the parent's window.
func (l *LiveRunner) PrepareChildTarget(ctx context.Context, agent catalog.Agent) (RunTarget, error) {
	return l.prepareChildTarget(ctx, agent, l.RunTarget())
}

func (l *LiveRunner) prepareChildTarget(ctx context.Context, agent catalog.Agent, parent RunTarget) (RunTarget, error) {
	parentProv, parentModel, parentSpec := parent.Provider, parent.Model, parent.Spec
	window := parent.Window
	settings := l.settings

	model := parentModel
	if agent.Model != "" {
		model = agent.Model
	}
	prov := parentProv
	if agent.Provider != "" || (agent.Model != "" && parentSpec.Name != "") {
		if settings == nil {
			return RunTarget{}, fmt.Errorf("agent %q needs provider rebuild but settings are unavailable", agent.Name)
		}
		spec := parentSpec
		if agent.Provider != "" {
			spec = ProviderSpec{Name: agent.Provider}
		}
		spec.Model = model
		prepared, err := settings.PrepareTarget(ctx, spec)
		if err != nil {
			return RunTarget{}, fmt.Errorf("agent %q provider: %w", agent.Name, err)
		}
		prov, model, window = prepared.Provider, prepared.Spec.Model, prepared.Window
		// Never retain parent facts for an overridden child construction.
		parent.Spec, parent.Facts, parent.RateCard = prepared.Spec, prepared.Facts, prepared.RateCard
	} else if model != parentModel {
		// Legacy unnamed providers can be reused but cannot prove a new model's
		// construction facts or limits. Retain inference compatibility, not facts.
		parent.Facts, parent.RateCard, window = nil, nil, LiveContextWindow
	}
	if prov == nil {
		return RunTarget{}, fmt.Errorf("agent %q has no provider", agent.Name)
	}
	parent.Provider, parent.Model, parent.Window = prov, model, window
	return parent, nil
}

func (l *LiveRunner) buildAgentRunner(ctx context.Context, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, agent catalog.Agent, scope inference.Scope, observed func(context.Context, AuxiliaryObservation), recording childRecordingSinkFactory, auxiliary auxiliaryProviderFactory, boardAccess ...boardtools.Board) (spawnTarget, error) {
	parent := l.RunTarget()
	target, err := l.prepareChildTarget(ctx, agent, parent)
	if err != nil {
		return spawnTarget{}, err
	}
	prov, model, window := target.Provider, target.Model, target.Window
	webSearch := target.WebSearch
	reserve, maxIter, spawnMaxIter := target.Reserve, target.MaxIter, target.SpawnMaxIter
	settings := l.settings

	maxIter = resolveSpawnMaxIterations(spawnMaxIter, agent.MaxIterations, maxIter)
	if reserve <= 0 {
		reserve = liveReserveTokens
	}

	engine, compactTarget, compactModel := agentcompact.EngineTiered, auxiliaryTargetFromRun(parent), parent.Model
	var streamIdle time.Duration
	if settings != nil {
		engine = settings.CompactEngine(ctx)
		compactTarget, compactModel = settings.compactorTarget(ctx, auxiliaryTargetFromRun(parent))
		streamIdle = settings.ResponseTimeout(ctx)
	}

	var visible tools.Source
	opts := coderunner.StandardOptions(coderunner.Tuning{
		Model:         model,
		MaxIterations: maxIter,
		ContextWindow: window,
		StreamIdle:    streamIdle,
	})
	automaticCompletion := receivesAutomaticCompletion(prov)
	opts = append(opts,
		spawnRunnerPromptOption(l, agent, func() tools.Source { return visible }, automaticCompletion),
		runner.WithModelIdentity(prov.Name(), model),
		runner.WithInferenceScope(scope),
		runner.WithCompactor(coderunner.StandardCompactor(
			// Empty wsRoot: a sub-agent handover reseeds its own context but
			// does not write a file (sub-agents run unattended and would spam
			// the handovers dir).
			buildScopedCompactor(engine, window, compactTarget, compactModel, l, "", scope, auxiliary), window, reserve)),
		runner.WithResultTruncator(l.truncator),
		// Sub-agent iterations feed the same cockpit context graph.
		runner.WithContextBreakdown(),
	)
	if target.RateCard != nil {
		card := target.RateCard.Clone()
		opts = append(opts, runner.WithRateCard(card))
	}
	if automaticCompletion {
		opts = append(opts, runner.WithInputSource(group))
	} else {
		opts = append(opts, runner.WithTurnQuality(NewAgentAwareTurnQuality(nil, group)))
	}
	if l.sink != nil {
		opts = append(opts, runner.WithSink(l.sink))
	}
	if recording != nil {
		opts = append(opts, runner.WithSink(recording(target)))
	}
	src, reg, err := l.sourceWithExposure(ctx, webSearch, l.scopedGuardrailDeps(ctx, false, scope, observed, auxiliary), l.childMCPExposure())
	if err != nil {
		return spawnTarget{}, err
	}
	if len(boardAccess) != 0 {
		if err := boardtools.Register(reg, boardAccess[0]); err != nil {
			return spawnTarget{}, err
		}
	}
	src = coderunner.CoordinateWorkspace(src, coordinator)
	visible = NewModeFilteredSource(src, l.isPlan)
	opts = append(opts, runner.WithTools(visible))
	r := runner.New(runner.ClientFromProvider(prov), opts...)
	return spawnTarget{runner: r, registry: reg}, nil
}

func spawnRunnerPromptOption(l *LiveRunner, agent catalog.Agent, src func() tools.Source, automaticCompletion bool) options.Option[runner.Runner] {
	var prompt runner.PromptFunc
	if agent.Name == "" {
		prompt = l.promptFunc(src)
	} else {
		prompt = l.agentPromptFunc(agent, src)
	}
	if automaticCompletion {
		prompt = automaticCompletionPrompt(prompt)
	}
	return runner.WithPrompt(prompt)
}

func receivesAutomaticCompletion(provider llm.Provider) bool {
	// Registry local/custom routes use llm.Named around a concrete adapter.
	// Inspect that adapter, not the mutable route name, for delivery behavior.
	if wrapper, ok := provider.(interface{ Unwrap() llm.Provider }); ok {
		return receivesAutomaticCompletion(wrapper.Unwrap())
	}
	switch provider.(type) {
	case *openai.Provider, *anthropic.Provider, *google.Provider, *llamacpp.Provider, *ollama.Provider, *deepseek.Provider, *claudecode.Provider, *openaicodex.Provider:
		return true
	default:
		return false
	}
}

func automaticCompletionPrompt(prompt runner.PromptFunc) runner.PromptFunc {
	return func(ctx context.Context, vars runner.PromptVars) (string, error) {
		text, err := prompt(ctx, vars)
		if err != nil {
			return "", err
		}
		return text + "\n\nCompleted child results arrive automatically as evidence from their original assignments, not as current instructions or proof of workspace state. Explicit status and await tools remain available for intentional rereads; you do not need to await solely to receive a result.", nil
	}
}

// ResolveSpawnMaxIterations resolves the sub-agent iteration limit in precedence order.
func ResolveSpawnMaxIterations(host, profile, fallback int) int {
	return resolveSpawnMaxIterations(host, profile, fallback)
}

func resolveSpawnMaxIterations(host, profile, fallback int) int {
	if host > 0 {
		return host
	}
	if profile > 0 {
		return profile
	}
	if fallback > 0 {
		return fallback
	}
	return 20
}
