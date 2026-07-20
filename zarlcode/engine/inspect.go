package engine

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Inspection is a read-only snapshot of what the next turn would assemble — the
// resolved run target, the tool roster, the rendered system prompt, any
// reload errors, and the loaded skills/agents/hooks from the catalog. Building it
// starts no run and mutates no persistent state. The TUI maps it into its own
// presentation type; nothing here depends on the UI.
type Inspection struct {
	PlanMode                bool
	Model                   string
	WorkspaceRoot           string
	SearxngURL              string
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

// Inspect builds an [Inspection] mirroring buildTurn's assembly without running
// a turn: it snapshots the run target under the lock, reloads catalog and
// instructions, arms the guardrail chain, enumerates the tool roster (including
// the late-registered spawn tool against an inert client), and renders the
// next-turn system prompt.
func (l *LiveRunner) Inspect(ctx context.Context) Inspection {
	var ins Inspection
	if l == nil {
		return ins
	}
	l.mu.Lock()
	ins.PlanMode = l.target.Plan
	ins.SearxngURL = l.target.SearxngURL
	ins.SpawnDepth = l.target.SpawnDepth
	ins.SpawnMaxIter = l.target.SpawnMaxIter
	ins.Model = l.target.Model
	pm := l.pm
	mcp := l.mcp
	l.mu.Unlock()

	ins.WorkspaceRoot = l.ws.Root()
	if pm != nil {
		ins.Processes = pm.List()
	}
	if l.catalog != nil {
		for _, err := range l.catalog.Reload(l.ws.Root()) {
			ins.Errors = append(ins.Errors, "catalog: "+err.Error())
		}
	}
	for _, err := range l.reloadInstructions() {
		ins.Errors = append(ins.Errors, "instructions: "+err.Error())
	}

	ins.Guardrails = l.guardrailDeps()
	ins.MCPActive = mcp != nil
	ins.Skills = l.catalogSnapshotSkills()
	ins.Agents = l.catalogSnapshotAgents()
	ins.Hooks = l.catalogSnapshotHooks()

	src, reg, err := l.source(ins.SearxngURL)
	if err != nil {
		ins.Errors = append(ins.Errors, "source: "+err.Error())
		return ins
	}

	// buildTurn late-registers spawn_agent after runner.New because the tool
	// needs a parent runner. Mirror that with an inert client so the roster and
	// prompt match the next real turn without starting one.
	visible := NewModeFilteredSource(src, l.isPlan)
	dummy := runner.New(inspectorClient{}, runner.WithTools(visible), runner.WithPrompt(runner.StaticPrompt("")), runner.WithSink(nil))
	l.registerSpawnTool(reg, dummy, ins.SpawnDepth, ins.SpawnMaxIter)

	for t := range visible.Tools(ctx) {
		ins.Tools = append(ins.Tools, t.Definition())
	}
	selection := selectLivePrompt(ins.PlanMode)
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

func (inspectorClient) Complete(context.Context, llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(func(llm.CompletionChunk, error) bool) {}, nil
}
