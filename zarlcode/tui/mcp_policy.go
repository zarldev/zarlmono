package tui

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

// configuredMCPPolicy freezes operator configuration for this application lifetime.
// Model calls never refresh approvals; settings changes take effect after restart.
func configuredMCPPolicy(ctx context.Context, settings *engine.Settings) (dynamic.MCPConnectPolicyFunc, error) {
	rows, err := settings.Store.ListMCPServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read MCP approvals: %w", err)
	}
	approved := make(map[string]dynamic.MCPConnSpec, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		transport, err := dynamic.ParseTransport(row.Transport)
		if err != nil {
			continue
		}
		auth, err := resolveMCPAuthToken(ctx, settings, row.Name, row.AuthRequired)
		if err != nil {
			continue
		} // Unavailable credentials never grant authority.
		approved[row.Name] = dynamic.MCPConnSpec{Type: transport, Command: row.Command, Args: row.Args, Env: row.Env, BaseURL: row.BaseURL, AuthToken: auth.token}
	}
	return engine.NewMCPConnectPolicy(approved), nil
}
