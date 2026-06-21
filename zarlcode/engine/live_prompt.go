package engine

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"

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
// drift on operating instructions. The TUI additionally honors a user's
// ~/.zarlcode/prompt.md override at build time (see systemPromptBody).
var (
	LiveSystemPromptTemplate = prompts.System
	LivePlanPromptTemplate   = prompts.Plan
)

// promptTool is the name/description shape the prompt templates range over.
// Aliased to the canonical prompts type so callers (inspect, tests) and the
// shared renderer agree without a conversion hop.
type promptTool = prompts.ToolInfo

// promptFunc renders the build/plan-mode system prompt for a top-level turn.
// Build mode uses the user's ~/.zarlcode/prompt.md when present, falling back
// to the embedded default; plan mode uses the embedded plan prompt. Reloaded
// each turn so edits to prompt.md take effect without a restart.
func (l *LiveRunner) promptFunc(src func() tools.Source) runner.PromptFunc {
	return func(ctx context.Context, _ runner.PromptVars) (string, error) {
		l.mu.Lock()
		plan := l.target.Plan
		l.mu.Unlock()
		name, body := "system", systemPromptBody()
		if plan {
			name, body = "plan", prompts.Plan
		}
		return RenderLivePrompt(name, body, l.ws.Root(), l.catalogSnapshotSkills(), l.catalogSnapshotAgents(), l.instructionSnapshotDocs(), ToolInfoFromSource(ctx, src()))
	}
}

func (l *LiveRunner) agentPromptFunc(agent catalog.Agent, src func() tools.Source) runner.PromptFunc {
	return func(ctx context.Context, _ runner.PromptVars) (string, error) {
		return RenderLivePrompt("agent:"+agent.Name, agent.Body, l.ws.Root(), l.catalogSnapshotSkills(), l.catalogSnapshotAgents(), l.instructionSnapshotDocs(), ToolInfoFromSource(ctx, src()))
	}
}

// systemPromptBody returns the build-mode prompt body: the user's
// ~/.zarlcode/prompt.md when it exists and is non-empty, else the embedded
// default. A read error other than not-exist is logged and falls back to the
// embedded prompt so a permissions glitch can't strand the agent without a
// system prompt.
func systemPromptBody() string {
	path, err := home.RootPromptPath()
	if err != nil {
		slog.Warn("prompt: resolve prompt.md path; using embedded default", "err", err)
		return prompts.System
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return prompts.System
	case err != nil:
		slog.Warn("prompt: read prompt.md; using embedded default", "path", path, "err", err)
		return prompts.System
	}
	if len(data) == 0 {
		return prompts.System
	}
	return string(data)
}

// RenderLivePrompt renders one of the prompt bodies against the live workspace
// state via the shared prompts.Render. The SelfMod / Planning capability flags
// are derived from the actual tool roster so the self-modification material and
// the update_plan contract only appear when the matching tools are registered.
func RenderLivePrompt(name, body, wsRoot string, skills []catalog.Skill, agents []catalog.Agent, instructionDocs []instructions.Document, toolInfo []promptTool) (string, error) {
	data := prompts.Data{
		WorkspaceRoot:   wsRoot,
		Tools:           toolInfo,
		InstructionDocs: promptInstructionDocs(instructionDocs),
		SelfMod:         prompts.HasTool(toolInfo, "new_tool") || prompts.HasTool(toolInfo, "register_tool"),
		Planning:        prompts.HasTool(toolInfo, "update_plan"),
	}
	return prompts.Render(name, body, data)
}

func (l *LiveRunner) instructionSnapshotDocs() []instructions.Document {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]instructions.Document(nil), l.instructionDocs...)
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
