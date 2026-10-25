package engine

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

// DeferredMCP reports whether connected remote schemas require explicit
// activation. Deferred exposure is the default; invalid persisted values reject
// tool assembly instead of silently broadening the advertised surface.
func (s *Settings) DeferredMCP(ctx context.Context) (bool, error) {
	switch value := s.setting(ctx, prefs.KeyMCPToolExposure, prefs.DefaultMCPToolExposure); value {
	case "direct":
		return false, nil
	case "deferred":
		return true, nil
	default:
		return false, fmt.Errorf("MCP tool exposure %q: expected direct or deferred", value)
	}
}
