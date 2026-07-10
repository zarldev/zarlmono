package guardrails_test

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ExampleNewGuardedSource arms a fan-out cap around a tool source: reads under
// the cap pass through, and the call that reaches it is converted into a failed
// result the model can read and react to — never a hard error.
func ExampleNewGuardedSource() {
	reg := tools.NewRegistry(runnertest.Tool{Name: "read", Result: "file contents"})
	guarded := guardrails.NewGuardedSource(reg,
		guardrails.NewFanoutGuardrail(map[tools.ToolName]int{"read": 2}),
	)

	for i := 1; i <= 2; i++ {
		call := tools.ToolCall{ID: tools.ToolCallID(fmt.Sprintf("c%d", i)), ToolName: "read", Arguments: tools.ToolParameters{}}
		res, err := guarded.Execute(context.Background(), call)
		fmt.Printf("call %d: err=%v success=%v\n", i, err, res.Success)
	}
	// Output:
	// call 1: err=<nil> success=true
	// call 2: err=<nil> success=false
}
