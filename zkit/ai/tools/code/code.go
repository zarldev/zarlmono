package code

import (
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool name constants for the file-editing toolset.
const (
	ToolNameBash           tools.ToolName = "bash"
	ToolNameRead           tools.ToolName = "read"
	ToolNameWrite          tools.ToolName = "write"
	ToolNameWriteAppend    tools.ToolName = "write_append"
	ToolNameEdit           tools.ToolName = "edit"
	ToolNameLs             tools.ToolName = "ls"
	ToolNameGrep           tools.ToolName = "grep"
	ToolNameGlob           tools.ToolName = "glob"
	ToolNameSavePlan       tools.ToolName = "save_plan"
	ToolNameSavePlanAppend tools.ToolName = "save_plan_append"
	ToolNameApplyPatch     tools.ToolName = "apply_patch"
	ToolNameUpdatePlan     tools.ToolName = "update_plan"

	// Long-running process management — paired with bash background=true.
	ToolNameBashOutput    tools.ToolName = "bash_output"
	ToolNameStopProcess   tools.ToolName = "stop_process"
	ToolNameListProcesses tools.ToolName = "list_processes"
)
