package taskrunner

import (
	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// CoderToolFactory produces the slice of file-editing tools bound to a
// given workspace. Each invocation returns fresh tool instances — the
// runner calls the factory once per task so tools never leak state
// between tasks.
type CoderToolFactory func(ws code.Workspace) []tools.Tool

// NewCoderToolFactory returns a factory that emits the canonical six code
// tools (read/write/edit/grep/ls/bash) bound to the supplied workspace.
func NewCoderToolFactory() CoderToolFactory {
	return func(ws code.Workspace) []tools.Tool {
		return []tools.Tool{
			code.NewReadTool(ws),
			code.NewWriteTool(ws),
			code.NewEditTool(ws),
			code.NewGrepTool(ws),
			code.NewLsTool(ws),
			code.NewBashTool(ws),
		}
	}
}
