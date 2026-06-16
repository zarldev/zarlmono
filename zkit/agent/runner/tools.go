package runner

import (
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolSource is what the runner takes — read + dispatch, nothing else.
// Producers (the dynamic loader, the MCP bridge, the agent's own
// `register` tool) implement the wider ToolRegistry below; the runner
// only depends on this narrower view.
type ToolSource = tools.Source

// ToolRegistry is the producer-side contract: read, dispatch, and
// mutate. Anything that wants to add or drop tools at runtime takes
// this; the runner does not.
type ToolRegistry interface {
	tools.Source
	Register(tools.Tool)
	Unregister(tools.ToolName)
}
