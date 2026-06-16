package harness

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Worktree diff capture (tracked changes + synthesized diffs for files
// the agent created) lives in the SHARED code.WorktreeDiff /
// code.UntrackedFiles — the same capture zarlcode's headless recorder
// uses, so the submitted SWE-bench patch and the recorded session diff
// can't drift.

// roleAssistant is the assistant message role (matches llm.Message.Role).
const roleAssistant = "assistant"

// renderSystemPrompt renders the canonical system prompt (prompts.System)
// against the registered tool inventory via the SHARED prompts.Render — the
// same renderer the TUI uses — so the eval agent and the interactive agent
// can't drift on operating instructions. SWE-bench worktrees register no
// skills, sub-agents, or self-mod/plan tools, so those template blocks render
// empty; the SelfMod / Planning flags are derived from the live roster so they
// track reality rather than being hardcoded.
func renderSystemPrompt(workspaceRoot string, reg *tools.Registry) (string, error) {
	data := prompts.Data{WorkspaceRoot: workspaceRoot}
	for tool := range reg.Tools(context.Background()) {
		spec := tool.Definition()
		data.Tools = append(data.Tools, prompts.ToolInfo{
			Name:        string(spec.Name),
			Description: spec.Description,
		})
	}
	data.SelfMod = prompts.HasTool(data.Tools, "new_tool") || prompts.HasTool(data.Tools, "register_tool")
	data.Planning = prompts.HasTool(data.Tools, "update_plan")
	return prompts.Render("system", prompts.System, data)
}
