package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/home"
	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// LiveSystemPromptTemplate and LivePlanPromptTemplate are the canonical
// build/plan-mode prompt bodies (zarlcode/prompts). They are the SAME assets
// the eval harness renders, so the interactive agent and the eval agent can't
// drift on operating instructions. The TUI additionally resolves per-user
// preferences and explicit/legacy build-prompt overrides at turn time.
var (
	LiveSystemPromptTemplate = prompts.System
	LivePlanPromptTemplate   = prompts.Plan
)

// promptTool is the name/description shape the prompt templates range over.
// Aliased to the canonical prompts type so callers (inspect, tests) and the
// shared renderer agree without a conversion hop.
type promptTool = prompts.ToolInfo

type livePromptSelection struct {
	Name              string
	Body              string
	BodySource        string
	Preferences       string
	PreferencesSource string
	ResolutionMode    home.PromptResolutionMode
	Diagnostics       []string
}

func selectLivePrompt(plan bool) livePromptSelection {
	resolved := home.ResolveBuildPrompt(prompts.System)
	selection := livePromptSelection{
		Name:           "system",
		Body:           resolved.Body,
		BodySource:     resolved.BodySource,
		ResolutionMode: resolved.Mode,
		Diagnostics:    append([]string(nil), resolved.Diagnostics...),
	}
	if resolved.UsePreferences {
		selection.Preferences = resolved.Preferences
		selection.PreferencesSource = resolved.PreferencesSource
	}
	if plan {
		selection.Name = "plan"
		selection.Body = prompts.Plan
		selection.BodySource = "embedded plan prompt"
		selection.Preferences = resolved.Preferences
		selection.PreferencesSource = resolved.PreferencesSource
	}
	return selection
}

// promptFunc renders the build/plan-mode system prompt for a top-level turn.
// Build mode may use an explicit/legacy full override; plan mode always uses
// the embedded plan prompt. Literal preferences are reloaded each turn.
func (l *LiveRunner) promptFunc(src func() tools.Source) runner.PromptFunc {
	return func(ctx context.Context, _ runner.PromptVars) (string, error) {
		l.mu.Lock()
		plan := l.target.Plan
		l.mu.Unlock()

		selection := selectLivePrompt(plan)
		l.publishPromptDiagnostics(selection.Diagnostics)
		return RenderLivePrompt(selection.Name, selection.Body, l.ws.Root(), l.catalogSnapshotSkills(), l.catalogSnapshotAgents(), l.instructionSnapshotDocs(), ToolInfoFromSource(ctx, src()), selection.Preferences)
	}
}

func (l *LiveRunner) agentPromptFunc(agent catalog.Agent, src func() tools.Source) runner.PromptFunc {
	return func(ctx context.Context, _ runner.PromptVars) (string, error) {
		resolved := home.ResolveBuildPrompt(prompts.System)
		return RenderLivePrompt("agent:"+agent.Name, agent.Body, l.ws.Root(), l.catalogSnapshotSkills(), l.catalogSnapshotAgents(), l.instructionSnapshotDocs(), ToolInfoFromSource(ctx, src()), resolved.Preferences)
	}
}

type promptDiagnosticsSink interface {
	PromptDiagnostics([]string)
}

func (l *LiveRunner) publishPromptDiagnostics(diags []string) {
	if l == nil || len(diags) == 0 || l.sink == nil {
		return
	}
	sink, ok := l.sink.(promptDiagnosticsSink)
	if !ok {
		return
	}
	sink.PromptDiagnostics(diags)
}

// RenderLivePrompt renders one of the prompt bodies against the live workspace
// state via the shared prompts.Render. Capability flags are derived from the
// actual tool roster so conditional guidance only appears when the matching
// tools are registered.
func RenderLivePrompt(name, body, wsRoot string, skills []catalog.Skill, agents []catalog.Agent, instructionDocs []instructions.Document, toolInfo []promptTool, userPreferences string) (string, error) {
	canAuthorTool := prompts.HasTool(toolInfo, "new_tool")
	canRegisterTool := prompts.HasTool(toolInfo, "register_tool")
	data := prompts.Data{
		WorkspaceRoot:     wsRoot,
		Tools:             toolInfo,
		InstructionDocs:   promptInstructionDocs(instructionDocs),
		SelfMod:           canAuthorTool || canRegisterTool,
		CanAuthorTool:     canAuthorTool,
		CanRegisterTool:   canRegisterTool,
		Planning:          prompts.HasTool(toolInfo, "update_plan"),
		ProgrammaticTools: prompts.HasTool(toolInfo, "program"),
		UserPreferences:   userPreferences,
	}
	return prompts.Render(name, body, data)
}

type promptStackSources struct {
	BodySource            string
	UserPreferences       string
	UserPreferencesSource string
}

func BuildPromptStack(name, body, rendered string, skills []catalog.Skill, agents []catalog.Agent, instructionDocs []instructions.Document) prompts.Stack {
	return buildPromptStackWithSources(name, body, rendered, promptStackSources{}, skills, agents, instructionDocs)
}

func buildPromptStackWithSources(name, body, rendered string, sources promptStackSources, skills []catalog.Skill, agents []catalog.Agent, instructionDocs []instructions.Document) prompts.Stack {
	kind := prompts.FragmentSystem
	source := sources.BodySource
	if source == "" {
		source = "embedded system prompt or user override"
	}
	if name == "plan" || name == "inspector:plan" {
		kind = prompts.FragmentPlan
		if sources.BodySource == "" {
			source = "embedded plan prompt"
		}
	}
	order := 0
	fragments := []prompts.Fragment{
		prompts.NewFragment(kind, name, source, "active prompt body", order, body, true),
	}
	order++
	if strings.TrimSpace(sources.UserPreferences) != "" {
		prefSource := sources.UserPreferencesSource
		if prefSource == "" {
			prefSource = fmt.Sprintf("~/.zarlcode/%s", home.PreferencesFile)
		}
		fragments = append(fragments, prompts.NewFragment(prompts.FragmentUserPreferences, home.PreferencesFile, prefSource, "literal user preferences appended to prompt", order, sources.UserPreferences, true))
		order++
	}
	for _, doc := range instructionDocs {
		fragments = append(fragments, prompts.NewFragment(prompts.FragmentWorkspaceInstruction, doc.RelPath, doc.RelPath, "workspace instruction appended to prompt", order, doc.Content, true))
		order++
	}
	for _, skill := range skills {
		fragments = append(fragments, prompts.NewFragment(prompts.FragmentSkill, skill.Name, skill.Source, "catalogued skill; loaded on demand", order, skill.Body, false))
		order++
	}
	for _, agent := range agents {
		fragments = append(fragments, prompts.NewFragment(prompts.FragmentAgent, agent.Name, agent.Source, "catalogued sub-agent; used when delegated", order, agent.Body, false))
		order++
	}
	fragments = append(fragments, prompts.NewFragment(prompts.FragmentRenderedTotal, name, "rendered prompt", "fully rendered system message", order, rendered, true))
	return prompts.NewStack(fragments)
}

func (l *LiveRunner) instructionSnapshotDocs() []instructions.Document {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]instructions.Document(nil), l.instructionDocs...)
}

func (l *LiveRunner) instructionNestedSnapshot() []instructions.NestedDoc {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]instructions.NestedDoc(nil), l.nestedInstructionIndex...)
}

func (l *LiveRunner) catalogSnapshotSkills() []catalog.Skill {
	if l == nil || l.catalog == nil {
		return nil
	}
	return l.catalog.Skills()
}

func (l *LiveRunner) catalogSnapshotAgents() []catalog.Agent {
	if l == nil || l.catalog == nil {
		return nil
	}
	return l.catalog.Agents()
}

func (l *LiveRunner) catalogSnapshotHooks() []catalog.Hook {
	if l == nil || l.catalog == nil {
		return nil
	}
	return l.catalog.Hooks()
}

func promptInstructionDocs(docs []instructions.Document) []prompts.InstructionDoc {
	out := make([]prompts.InstructionDoc, 0, len(docs))
	for _, doc := range docs {
		out = append(out, prompts.InstructionDoc{Path: doc.RelPath, Content: doc.Content})
	}
	return out
}

func ToolInfoFromSource(ctx context.Context, src tools.Source) []promptTool {
	if src == nil {
		return nil
	}
	out := []promptTool{}
	for t := range src.Tools(ctx) {
		def := t.Definition()
		out = append(out, promptTool{Name: def.Name.String(), Description: def.Description})
	}
	return out
}
