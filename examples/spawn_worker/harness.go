package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const parentSystemPrompt = `You are a coordinator agent. Your job is to manage complex tasks by delegating to specialized worker agents.

You have access to three specialist workers:
- "researcher" (explore mode): analyzes code, identifies relevant files, understands current implementation
- "reviewer" (verify mode): evaluates plans, checks for risks, validates approaches
- "coder" (implement mode): writes and modifies code

Use spawn_agent to delegate work:
- Call researcher first to understand the codebase
- Call reviewer to validate your plan
- Call coder to implement changes
- You can spawn multiple workers in parallel if their work is independent

When all workers complete, verify the results and report success.`

const goalPrompt = "Refactor the authentication system from session-based to JWT-based authentication."

// RunSpawnWorker demonstrates hierarchical agent decomposition.
// The parent coordinates the refactor by spawning specialized child agents.
func RunSpawnWorker(ctx context.Context, client runner.Client, fs *FileSystem, maxAttempts int) pursue.Outcome {
	// Build the parent registry with file tools + spawn_agent
	parentReg := tools.NewRegistry()
	_ = parentReg.Register(&readFileTool{fs: fs})
	_ = parentReg.Register(&listFilesTool{fs: fs})

	// Create the parent runner first (needed for spawn tool)
	parentRunner := runner.New(client,
		runner.WithTools(parentReg),
		runner.WithPromptText(parentSystemPrompt),
	)

	// Add spawn_agent tool with agent resolver and mode policy
	// The spawn tool uses the parent's runner for children
	spawnTool := spawn.New(parentRunner,
		spawn.WithMaxDepth(1), // Prevent grandchildren
		spawn.WithAgentResolver(BuildAgentResolver(fs, client)),
		spawn.WithModeToolPolicy(ModePolicy()),
	)
	_ = parentReg.Register(spawnTool)

	// Goal: the refactor is complete when we have JWT files and modified auth
	checkpoint := fs.Checkpoint()
	goal := pursue.GoalFunc(func(_ context.Context, attempt pursue.Attempt) pursue.Decision {
		// Check if the filesystem shows the refactor is complete
		if fs.RefactorComplete() {
			return pursue.Done()
		}

		// Provide feedback based on what we see
		modified := fs.ModifiedSince(checkpoint)
		if len(modified) == 0 {
			return pursue.Retry("No changes made yet. Spawn the researcher to understand the current auth system, then the coder to implement JWT.")
		}
		return pursue.Retry(fmt.Sprintf("Changes so far: %v. Continue coordination until JWT refactor is complete.", modified))
	})

	return pursue.Drive(ctx, pursue.NewRequest(parentRunner.Run,
		runner.TaskSpec{ID: "spawn-worker", Prompt: goalPrompt},
		pursue.WithGoal(goal),
	),
		pursue.WithMaxAttempts(maxAttempts),
		pursue.WithOnAttempt(func(report pursue.AttemptReport) {
			fmt.Fprintf(os.Stderr, "attempt %d/%d: %s\n",
				report.Attempt.Number, maxAttempts, pursue.LabelAttempt(report))
		}),
	)
}
