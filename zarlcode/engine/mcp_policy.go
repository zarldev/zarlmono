package engine

import (
	"context"
	"maps"
	"slices"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

// NewMCPConnectPolicy snapshots operator-approved connection specifications.
// Approval binds the name, transport, executable, arguments, environment, endpoint,
// and credential. It never replaces the built-in transport restrictions.
// Approved stdio servers are trusted host processes, not shell-sandboxed tools.
func NewMCPConnectPolicy(approved map[string]dynamic.MCPConnSpec) dynamic.MCPConnectPolicyFunc {
	snapshot := make(map[string]dynamic.MCPConnSpec, len(approved))
	for name, spec := range approved {
		spec.Args = slices.Clone(spec.Args)
		spec.Env = maps.Clone(spec.Env)
		snapshot[name] = spec
	}
	return func(ctx context.Context, name string, proposed dynamic.MCPConnSpec) error {
		if err := dynamic.DefaultMCPConnectPolicy.ValidateMCPConnect(ctx, name, proposed); err != nil {
			return err
		}
		spec, ok := snapshot[name]
		if !ok || spec.Type != proposed.Type || spec.Command != proposed.Command ||
			!slices.Equal(spec.Args, proposed.Args) || !maps.Equal(spec.Env, proposed.Env) ||
			spec.BaseURL != proposed.BaseURL || spec.AuthToken != proposed.AuthToken {
			return tools.Permission("mcp_connect", "connection is not operator-approved; configure the exact server and restart")
		}
		return nil
	}
}
