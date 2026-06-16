package spawn_test

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
)

// Construct a spawn tool with the default depth ceiling (1) and
// register it on a tool registry. Use spawn.WithMaxDepth to override.
func ExampleNew() {
	// In real usage, parent is your *runner.Runner; nil here for
	// godoc rendering.
	tool := spawn.New(nil)
	fmt.Println(tool.Definition().Name)
	// Output: spawn_agent
}

// WithMaxDepth tightens the recursion ceiling for consumers that
// want shallow agents — a HTTP endpoint that should never spawn,
// for instance, sets it to 0.
func ExampleWithMaxDepth() {
	tool := spawn.New(nil, spawn.WithMaxDepth(0))
	_ = tool // tool refuses every call when maxDepth is 0
	fmt.Println("max depth 0: spawn always refuses")
	// Output: max depth 0: spawn always refuses
}
