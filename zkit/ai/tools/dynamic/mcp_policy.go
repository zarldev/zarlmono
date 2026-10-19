package dynamic

import (
	"context"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// NewMCPAllowlistPolicy snapshots exact operator-approved server specifications.
// Approval binds the name and every connection field. Approved loopback HTTP
// endpoints may connect, but other transport restrictions (including cleartext
// bearer-token rejection) still apply. Unknown or changed specifications cannot
// gain authority from the snapshot. Arguments and environment maps are cloned.
func NewMCPAllowlistPolicy(approved map[string]MCPConnSpec) MCPConnectPolicyFunc {
	snapshot := make(map[string]MCPConnSpec, len(approved))
	for name, spec := range approved {
		spec.Args = slices.Clone(spec.Args)
		spec.Env = maps.Clone(spec.Env)
		snapshot[name] = spec
	}
	return func(ctx context.Context, name string, proposed MCPConnSpec) error {
		spec, ok := snapshot[name]
		if !ok || spec.Type != proposed.Type || spec.Command != proposed.Command ||
			!slices.Equal(spec.Args, proposed.Args) || !maps.Equal(spec.Env, proposed.Env) ||
			spec.BaseURL != proposed.BaseURL || spec.AuthToken != proposed.AuthToken {
			return tools.Permission("mcp_connect", "connection is not operator-approved; configure the exact server and restart")
		}
		if err := proposed.Validate(); err != nil {
			return err
		}
		return validateMCPTransport(ctx, proposed, true)
	}
}

func isMCPLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Zone() == "" && addr.Unmap().IsLoopback()
}
