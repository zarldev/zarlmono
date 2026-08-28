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
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

func (l *LiveRunner) registerSpawnTools(ctx context.Context, reg *tools.Registry, parent *runner.Runner, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, maxDepth, spawnMaxIter int) {
	if reg == nil || parent == nil || group == nil || maxDepth == 0 {
		return
	}
	if l.settings != nil && !l.settings.SpawnEnabled(ctx) {
		return
	}
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
	modes := SpawnModesConfig{}
	fallback := spawn.FallbackPlanner
	if l.settings != nil {
		modes = l.settings.SpawnModes(ctx)
		fallback = spawn.FallbackPolicy(l.settings.SpawnFallback(ctx))
	}
	var planner spawn.SpawnPlanner
	var candidates []spawn.AgentCandidate
	var plannerProv llm.Provider
	if l != nil {
		l.mu.Lock()
		plannerProv = l.target.Provider
		l.mu.Unlock()
	}
	if l != nil && plannerProv != nil && l.catalog != nil {
		for _, agent := range l.catalog.Agents() {
			candidates = append(candidates, spawn.AgentCandidate{
				Name:        agent.Name,
				Description: agent.Description,
				Mode:        spawn.SpawnMode(agent.Mode),
			})
		}
		if len(candidates) > 0 {
			planner = spawn.NewLLMSpawnPlanner(plannerProv)
		}
	}
	exploreTarget := l.buildDefaultSpawnTarget(ctx, group, coordinator, modes.Explore)
	verifyTarget := l.buildDefaultSpawnTarget(ctx, group, coordinator, modes.Verify)
	implementTarget := l.buildDefaultSpawnTarget(ctx, group, coordinator, modes.Implement)
	base := spawn.New(parent,
		spawn.WithMaxDepth(maxDepth),
		spawn.WithAgentResolver(func(name string) (*runner.Runner, error) { return l.resolveAgentRunner(ctx, group, coordinator, name) }),
		spawn.WithSpawnPlannerCandidates(planner, candidates),
		spawn.WithSpawnMaxIterations(spawnMaxIter),
		spawn.WithModeToolPolicy(coderunner.SpawnModePolicy()),
		spawn.WithDefaultAgent(spawn.SpawnModeExplore, modes.Explore.DefaultAgent),
		spawn.WithDefaultAgent(spawn.SpawnModeVerify, modes.Verify.DefaultAgent),
		spawn.WithDefaultAgent(spawn.SpawnModeImplement, modes.Implement.DefaultAgent),
		spawn.WithDefaultTarget(spawn.SpawnModeExplore, exploreTarget),
		spawn.WithDefaultTarget(spawn.SpawnModeVerify, verifyTarget),
		spawn.WithDefaultTarget(spawn.SpawnModeImplement, implementTarget),
		spawn.WithModeMaxIterations(spawn.SpawnModeExplore, modes.Explore.MaxIterations),
		spawn.WithModeMaxIterations(spawn.SpawnModeVerify, modes.Verify.MaxIterations),
		spawn.WithModeMaxIterations(spawn.SpawnModeImplement, modes.Implement.MaxIterations),
		spawn.WithFallbackPolicy(fallback),
	)
	for _, tool := range []tools.Tool{
		spawn.NewAsync(base, group, coordinator),
		spawn.NewAwait(group, spawn.WithAwaitTimeout(awaitTimeout), spawn.WithAwaitMaxTimeout(awaitMaxTimeout)),
		spawn.NewStatus(group),
		spawn.NewStop(group),
		spawn.NewList(group),
	} {
		_ = reg.Register(tool)
	}
}

func (l *LiveRunner) buildDefaultSpawnTarget(ctx context.Context, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, mode SpawnModeConfig) *runner.Runner {
	if mode.DefaultAgent != "" || mode.DefaultTarget.Name == "" && mode.DefaultTarget.Model == "" {
		return nil
	}
	target, err := l.buildAgentRunner(ctx, group, coordinator, catalog.Agent{
		Provider: mode.DefaultTarget.Name,
		Model:    mode.DefaultTarget.Model,
	})
	if err != nil {
		slog.WarnContext(ctx, "spawn: default mode target unavailable", "provider", mode.DefaultTarget.Name, "model", mode.DefaultTarget.Model, "err", err)
		return nil
	}
	return target
}

func (l *LiveRunner) resolveAgentRunner(ctx context.Context, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, name string) (*runner.Runner, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("empty agent name")
	}
	if l == nil || l.catalog == nil {
		return nil, errors.New("no agent catalog configured")
	}
	l.catalog.Reload(l.ws.Root())
	agent, ok := l.catalog.Agent(name)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", name)
	}
	return l.buildAgentRunner(ctx, group, coordinator, agent)
}

func (l *LiveRunner) buildAgentRunner(ctx context.Context, group *spawn.Group, coordinator *tools.WorkspaceCoordinator, agent catalog.Agent) (*runner.Runner, error) {
	l.mu.Lock()
	parentProv, parentModel, parentSpec := l.target.Provider, l.target.Model, l.target.Spec
	window, webSearch := l.target.Window, l.target.WebSearch
	reserve, maxIter, spawnMaxIter, spawnDepth := l.target.Reserve, l.target.MaxIter, l.target.SpawnMaxIter, l.target.SpawnDepth
	settings := l.settings
	l.mu.Unlock()

	model := parentModel
	if agent.Model != "" {
		model = agent.Model
	}
	prov := parentProv
	if agent.Provider != "" || (agent.Model != "" && parentSpec.Name != "") {
		if settings == nil {
			return nil, fmt.Errorf("agent %q needs provider rebuild but settings are unavailable", agent.Name)
		}
		spec := parentSpec
		if agent.Provider != "" {
			spec = ProviderSpec{Name: agent.Provider}
		}
		spec.Model = model
		built, err := BuildProvider(ctx, settings.Registry, settings.Svc, spec)
		if err != nil {
			return nil, fmt.Errorf("agent %q provider: %w", agent.Name, err)
		}
		prov = built
	}
	if prov == nil {
		return nil, fmt.Errorf("agent %q has no provider", agent.Name)
	}
	maxIter = resolveSpawnMaxIterations(spawnMaxIter, agent.MaxIterations, maxIter)
	if reserve <= 0 {
		reserve = liveReserveTokens
	}

	engine, compactProv, compactModel := agentcompact.EngineTiered, parentProv, parentModel
	var streamIdle time.Duration
	if settings != nil {
		engine = settings.CompactEngine(ctx)
		compactProv, compactModel = settings.CompactorProvider(ctx, parentProv, parentModel)
		streamIdle = settings.ResponseTimeout(ctx)
	}

	var visible tools.Source
	opts := coderunner.StandardOptions(coderunner.Tuning{
		Model:         model,
		MaxIterations: maxIter,
		ContextWindow: window,
		StreamIdle:    streamIdle,
	})
	opts = append(opts,
		spawnRunnerPromptOption(l, agent, func() tools.Source { return visible }),
		runner.WithCompactor(coderunner.StandardCompactor(
			// Empty wsRoot: a sub-agent handover reseeds its own context but
			// does not write a file (sub-agents run unattended and would spam
			// the handovers dir).
			buildLiveCompactor(engine, window, compactProv, compactModel, l, ""), window, reserve)),
		runner.WithResultTruncator(l.truncator),
		// Sub-agent iterations feed the same cockpit context graph.
		runner.WithContextBreakdown(),
	)
	if l.sink != nil {
		opts = append(opts, runner.WithSink(l.sink))
	}
	src, reg, err := l.source(ctx, webSearch)
	if err != nil {
		return nil, err
	}
	src = coderunner.CoordinateWorkspace(src, coordinator)
	visible = NewModeFilteredSource(src, l.isPlan)
	opts = append(opts, runner.WithTools(visible))
	r := runner.New(runner.ClientFromProvider(prov), opts...)
	l.registerSpawnTools(ctx, reg, r, group, coordinator, spawnDepth, spawnMaxIter)
	return r, nil
}

func spawnRunnerPromptOption(l *LiveRunner, agent catalog.Agent, src func() tools.Source) options.Option[runner.Runner] {
	if agent.Name == "" {
		return runner.WithPrompt(l.promptFunc(src))
	}
	return runner.WithPrompt(l.agentPromptFunc(agent, src))
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
