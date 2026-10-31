package engine

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Inspection previews turn assembly using the latest published discovery
// snapshots: the resolved run target, tool roster, rendered system prompt,
// discovery errors, and loaded skills/agents/hooks. It starts no run and mutates
// no persistent state. The TUI maps it into its own presentation type.
type Inspection struct {
	PlanMode                bool
	Model                   string
	WorkspaceRoot           string
	SpawnDepth              int
	SpawnMaxIter            int
	Processes               []code.ProcessInfo
	Guardrails              guardrails.Deps
	MCPActive               bool
	Tools                   []tools.ToolSpec
	PromptSystem            string
	PromptStack             prompts.Stack
	PromptSource            string
	PromptPreferencesSource string
	PromptResolutionMode    home.PromptResolutionMode
	Errors                  []string
	Skills                  []catalog.Skill
	Agents                  []catalog.Agent
	// Hooks is the discovered command-hook catalog — what the next turn's
	// hook guardrail will arm.
	Hooks []catalog.Hook
}

// Inspect builds an [Inspection] without reloading catalog or instruction
// files. Startup, turn assembly, and explicit catalog tools publish discovery
// snapshots; inspection uses those snapshots to avoid filesystem traversal in
// UI rendering. It still assembles the current guarded tool roster and prompt.
// An exclusive reservation or shutdown returns an unavailable diagnostic.
func (l *LiveRunner) Inspect(ctx context.Context) Inspection {
	var ins Inspection
	if l == nil {
		return ins
	}
	if !l.admission.enter() {
		ins.Errors = []string{l.admission.rejection().Error()}
		return ins
	}
	defer l.admission.leave()
	l.mu.Lock()
	ins.PlanMode = l.target.Plan
	webSearch := l.target.WebSearch
	ins.SpawnDepth = l.target.SpawnDepth
	ins.SpawnMaxIter = l.target.SpawnMaxIter
	ins.Model = l.target.Model
	profile := l.promptProfile
	pm := l.pm
	mcp := l.mcp
	for _, err := range l.instructionErrs {
		ins.Errors = append(ins.Errors, "instructions: "+err.Error())
	}
	l.mu.Unlock()

	ins.WorkspaceRoot = l.ws.Root()
	if pm != nil {
		ins.Processes = pm.List()
	}
	if l.catalog != nil {
		for _, err := range l.catalog.Errors() {
			ins.Errors = append(ins.Errors, "catalog: "+err.Error())
		}
	}

	ins.Guardrails = l.guardrailDeps(ctx)
	ins.MCPActive = mcp != nil
	ins.Skills = l.catalogSnapshotSkills()
	ins.Agents = l.catalogSnapshotAgents()
	ins.Hooks = l.catalogSnapshotHooks()

	src, reg, err := l.source(ctx, webSearch)
	if err != nil {
		ins.Errors = append(ins.Errors, "source: "+err.Error())
		return ins
	}

	// Turn assembly late-registers agent_spawn after runner.New because the tool
	// needs a parent runner. Mirror that with an inert client so the roster and
	// prompt match the next real turn without starting one.
	group := spawn.NewGroup()
	visible := newModeControlSource(NewModeFilteredSource(src, l.isPlan), &modeTransition{live: l, group: group})
	dummy := runner.New(inspectorClient{}, runner.WithTools(visible), runner.WithPrompt(runner.StaticPrompt("")), runner.WithSink(runner.NopSink{}))
	l.registerSpawnTools(ctx, reg, dummy, group, nil, ins.SpawnDepth, ins.SpawnMaxIter, inference.Scope{})

	for t := range visible.Tools(ctx) {
		ins.Tools = append(ins.Tools, t.Definition())
	}
	selection := selectLivePrompt(ins.PlanMode, profile)
	ins.PromptSource = selection.BodySource
	ins.PromptPreferencesSource = selection.PreferencesSource
	ins.PromptResolutionMode = selection.ResolutionMode
	ins.Errors = append(ins.Errors, selection.Diagnostics...)
	promptSkills := l.catalogSnapshotSkills()
	promptAgents := l.catalogSnapshotAgents()
	promptDocs := l.instructionSnapshotDocs()
	prompt, err := RenderLivePrompt(selection.Name, selection.Body, l.ws.Root(), promptSkills, promptAgents, promptDocs, ToolInfoFromSource(ctx, visible), selection.Preferences)
	if err != nil {
		ins.Errors = append(ins.Errors, "prompt: "+err.Error())
	} else {
		ins.PromptSystem = prompt
		ins.PromptStack = buildPromptStackWithSources(selection.Name, selection.Body, prompt, promptStackSources{
			BodySource:            selection.BodySource,
			UserPreferences:       selection.Preferences,
			UserPreferencesSource: selection.PreferencesSource,
		}, promptSkills, promptAgents, promptDocs)
	}
	return ins
}

// inspectorClient is an inert runner.Client: it never produces output, so the
// inspector can build a runner purely to enumerate the tool roster and render
// the prompt without making a model call.
type inspectorClient struct{}

func (inspectorClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {}
}
