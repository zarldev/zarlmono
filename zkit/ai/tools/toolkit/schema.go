package toolkit

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// SchemaFor reflects over T and returns the tool's parameter schema (the shape
// tools.ToolSpec.Parameters expects) so a tool author never has to hand-write
// a schema tree to describe their args.
//
// SchemaFor is a compatibility wrapper around [tools.SchemaFor]. New built-in
// tool code should call tools.SchemaFor directly.
func SchemaFor[T any]() llm.Schema {
	return tools.SchemaFor[T]()
}
