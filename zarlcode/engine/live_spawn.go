package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func (l *LiveRunner) registerSpawnTool(reg *tools.Registry, parent *runner.Runner, maxDepth, spawnMaxIter int) {
	if reg == nil || parent == nil || maxDepth == 0 {
		return
	}
	var planner spawn.SpawnPlanner
	var names []string
	var plannerProv llm.Provider
	if l != nil {
		l.mu.Lock()
		plannerProv = l.target.Provider
		l.mu.Unlock()
	}
	if l != nil && plannerProv != nil && l.catalog != nil {
		names = l.catalog.AgentNames()
		if len(names) > 0 {
			planner = spawn.NewLLMSpawnPlanner(plannerProv)
		}
	}
	reg.Register(spawn.New(parent,
		spawn.WithMaxDepth(maxDepth),
		spawn.WithAgentResolver(l.resolveAgentRunner),
		spawn.WithSpawnPlanner(planner, names),
		spawn.WithSpawnMaxIterations(spawnMaxIter),
		// Arm the same explore/verify tool gating coderunner.RegisterSpawnTool
		// uses — without it the mode arg is advisory prompt text only.
		spawn.WithModeToolPolicy(coderunner.SpawnModePolicy()),
	))
}

func (l *LiveRunner) resolveAgentRunner(name string) (*runner.Runner, error) {
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
	return l.buildAgentRunner(agent)
}

func (l *LiveRunner) buildAgentRunner(agent catalog.Agent) (*runner.Runner, error) {
	l.mu.Lock()
	parentProv, parentModel, parentSpec := l.target.Provider, l.target.Model, l.target.Spec
	window, searxngURL := l.target.Window, l.target.SearxngURL
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
		built, err := BuildProvider(l.parentContext(), settings.Registry, settings.Svc, spec)
		if err != nil {
			return nil, fmt.Errorf("agent %q provider: %w", agent.Name, err)
		}
		prov = built
	}
	if prov == nil {
		return nil, fmt.Errorf("agent %q has no provider", agent.Name)
	}
	if spawnMaxIter > 0 {
		maxIter = spawnMaxIter
	} else if maxIter <= 0 {
		maxIter = 20
	}
	if agent.MaxIterations > 0 {
		maxIter = agent.MaxIterations
	}
	if reserve <= 0 {
		reserve = liveReserveTokens
	}

	engine, compactProv, compactModel := "tiered", parentProv, parentModel
	if settings != nil {
		ctx := l.parentContext()
		engine = settings.CompactEngine(ctx)
		compactProv, compactModel = settings.CompactorProvider(ctx, parentProv, parentModel)
	}

	var visible tools.Source
	opts := coderunner.StandardOptions(coderunner.Tuning{
		Model:         model,
		MaxIterations: maxIter,
		ContextWindow: window,
	})
	opts = append(opts,
		runner.WithPrompt(l.agentPromptFunc(agent, func() tools.Source { return visible })),
		runner.WithCompactor(coderunner.StandardCompactor(
			buildLiveCompactor(engine, window, compactProv, compactModel, l), window, reserve)),
		runner.WithResultTruncator(l.truncator),
		// Sub-agent iterations feed the same cockpit context graph.
		runner.WithContextBreakdown(),
	)
	if l.sink != nil {
		opts = append(opts, runner.WithSink(l.sink))
	}
	src, reg, err := l.source(searxngURL)
	if err != nil {
		return nil, err
	}
	visible = NewModeFilteredSource(src, l.isPlan)
	opts = append(opts, runner.WithTools(visible))
	r := runner.New(runner.ClientFromProvider(prov), opts...)
	l.registerSpawnTool(reg, r, spawnDepth, spawnMaxIter)
	return r, nil
}
