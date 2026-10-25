package engine

import "github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"

// NewMCPConnectPolicy snapshots operator-approved connection specifications.
// Approval binds the name, transport, executable, arguments, environment, endpoint,
// and credential. Exact approved loopback endpoints are allowed; all other
// built-in transport restrictions remain in force.
// Approved stdio servers are trusted host processes, not shell-sandboxed tools.
func NewMCPConnectPolicy(approved map[string]dynamic.MCPConnSpec) dynamic.MCPConnectPolicyFunc {
	return dynamic.NewMCPAllowlistPolicy(approved)
}
