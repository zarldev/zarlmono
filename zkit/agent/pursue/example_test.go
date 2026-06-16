package pursue_test

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// ExampleDrive re-drives an attempt toward a world-verifying Goal: the
// oracle checks real state (here, the deployed flag) rather than trusting
// the model's claim of completion, and the second attempt is re-driven
// with the Goal's corrective feedback as its prompt.
func ExampleDrive() {
	deployed := false
	attempts := 0

	// In production this is r.Run on a real runner; the contract is just
	// a function from TaskSpec to TaskResult.
	attempt := func(_ context.Context, spec runner.TaskSpec) runner.TaskResult {
		attempts++
		fmt.Printf("attempt %d prompt: %s\n", attempts, spec.Prompt)
		if attempts == 2 {
			deployed = true // the agent gets it right on the retry
		}
		return runner.TaskResult{Reason: runner.TerminalCompleted}
	}

	goal, _ := pursue.Until(func() bool { return deployed }, "not deployed yet — run the deploy step")

	out := pursue.Drive(context.Background(),
		pursue.NewRequest(attempt, runner.TaskSpec{Prompt: "deploy the service"}, pursue.WithGoal(goal)),
		pursue.WithMaxAttempts(3),
	)

	fmt.Println("status:", out.Status())
	fmt.Println("attempts:", out.Attempts)
	fmt.Println("verified:", out.Verified)
	// Output:
	// attempt 1 prompt: deploy the service
	// attempt 2 prompt: not deployed yet — run the deploy step
	// status: succeeded
	// attempts: 2
	// verified: true
}
