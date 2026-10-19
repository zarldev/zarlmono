package dynamic

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ValidateMCPToolRefs validates stored selection hints without connecting to a
// server or granting execution authority. Unresolved valid references are legal.
func ValidateMCPToolRefs(refs []MCPToolRef) error {
	if len(refs) > maxMCPDesiredTools {
		return tools.Budget("MCP selection", fmt.Sprintf("at most %d desired tools", maxMCPDesiredTools))
	}
	seen := make(map[MCPToolRef]struct{}, len(refs))
	for _, ref := range refs {
		if err := validateMCPToolRef(ref); err != nil {
			return err
		}
		if _, exists := seen[ref]; exists {
			return tools.Validation("MCP selection", "duplicate tool reference")
		}
		seen[ref] = struct{}{}
	}
	return nil
}
