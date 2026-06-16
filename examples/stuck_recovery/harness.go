package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const systemPrompt = `You are a code search agent. Your job is to find functions and symbols in the codebase.

Use the grep tool to search for patterns. If you don't find what you're looking for:
1. Try variations of the search pattern
2. Consider that the function might not exist
3. You can use spawn_agent with the "researcher" agent to do a broader exploration

Always verify your findings by reading the relevant files.`

// RunStuckRecovery demonstrates the DecomposeGuardrail graduated response.
// The agent searches for a non-existent function; the guardrail tracks
// failures and escalates from pass-through → advisory → fatal.
func RunStuckRecovery(ctx context.Context, client runner.Client, fs *FileSystem, attempts *SearchAttempts, maxAttempts int) pursue.Outcome {
	// Build tools
	reg := tools.NewRegistry()
	reg.Register(&grepTool{fs: fs, attempts: attempts})
	reg.Register(&listFilesTool{fs: fs})
	reg.Register(&readFileTool{fs: fs})

	// Build guardrails
	rails := BuildGuardrails(fs, client)
	source := guardrails.NewGuardedSource(reg, rails...)

	// Create runner
	r := runner.New(client,
		runner.WithTools(source),
		runner.WithPromptText(systemPrompt),
	)

	// Goal: find NonExistentHandler (it doesn't exist!)
	// The oracle verifies by checking the filesystem.
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		// Check if we've confirmed the function doesn't exist
		if !fs.HasFunction("NonExistentHandler") {
			// We've searched thoroughly enough - the function really doesn't exist
			// This is actually the expected outcome!
			if attempts.Count() >= 3 {
				return pursue.Done()
			}
		}
		return pursue.Retry("Keep searching or confirm the function doesn't exist")
	})

	return pursue.Drive(ctx, pursue.NewRequest(r.Run,
		runner.TaskSpec{ID: "stuck-recovery", Prompt: "Find the function NonExistentHandler"},
		pursue.WithGoal(goal),
	),
		pursue.WithMaxAttempts(maxAttempts),
		pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			fmt.Fprintf(os.Stderr, "attempt %d/%d: %s\n",
				report.Attempt.Number, maxAttempts, pursue.LabelAttempt(report))
		}),
	)
}
