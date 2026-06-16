package taskrunner

import (
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// BuiltinToolGates returns the registry-gate specs for the builtin
// profiles. Persona and execution settings live in profile.Builtin()
// (zkit); which tools each profile's task source sees is a registry
// concern, keyed here by profile name. Names that don't resolve at
// runtime are logged once and dropped by the ProfileRegistry.
func BuiltinToolGates() map[profile.Name]GateSpec {
	return map[profile.Name]GateSpec{
		// Dynamic: registry.All() minus task tools, plus action tools.
		profile.NameDefault: {},
		profile.NameResearcher: {
			Tools: []tools.ToolName{
				"web_search",
				"search_youtube",
				"wiki_search",
				"recall",
				"current_time",
				"store_memory",
				"notify_user",
				"present_findings",
			},
			// Every tool registered under the "obsidian" provider becomes
			// available to the researcher — letting it build a persistent
			// knowledge base in the vault without us enumerating tool names
			// here (and without breaking if the MCP server renames one).
			Providers: []string{"obsidian"},
		},
		profile.NameCoder: {
			Tools: []tools.ToolName{
				"read",
				"write",
				"edit",
				"grep",
				"ls",
				"bash",
				"current_time",
				"present_findings",
			},
		},
	}
}
