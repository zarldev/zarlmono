package computer

import (
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// NewTools returns the standard computer-use tool pair backed by observer and
// actor. Pass the same browser session for both arguments when using a backend
// that implements both capabilities.
func NewTools(observer model.Observer, actor model.Actor) []tools.Tool {
	return []tools.Tool{
		NewObserveTool(observer),
		NewActTool(actor),
	}
}

// Register adds computer_observe and computer_act to reg. It is a small helper
// for consumers that already own backend lifecycle and registry construction.
func Register(reg *tools.Registry, observer model.Observer, actor model.Actor) {
	if reg == nil {
		return
	}
	for _, tool := range NewTools(observer, actor) {
		reg.Register(tool)
	}
}
