package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// NewScriptedClient creates a deterministic client that simulates the model
// calling spawn_agent with different workers in sequence.
func NewScriptedClient(fs *FileSystem) runner.Client {
	_ = fs // reserved for future use — may validate script against filesystem state
	// This script simulates a successful multi-agent workflow:
	// 1. Parent spawns researcher (explore)
	// 2. Parent spawns reviewer (verify)
	// 3. Parent spawns coder (implement)
	// 4. Parent verifies and completes
	turns := [][]llm.CompletionChunk{
		// Turn 1: Spawn researcher to understand auth system
		{
			runnertest.ChunkToolCall("c1", "spawn_agent", `{"prompt": "Explore the current auth system. List files and read auth.go and session.go to understand how sessions work.", "agent": "researcher", "mode": "explore"}`),
			runnertest.ChunkDone(),
		},
		// Turn 2: Spawn reviewer to validate plan
		{
			runnertest.ChunkToolCall("c2", "spawn_agent", `{"prompt": "Review the plan to refactor to JWT. The researcher found session-based auth in auth.go. We will create jwt.go with JWT logic and modify auth.go to use it.", "agent": "reviewer", "mode": "verify"}`),
			runnertest.ChunkDone(),
		},
		// Turn 3: Spawn coder to implement
		{
			runnertest.ChunkToolCall("c3", "spawn_agent", `{"prompt": "Implement JWT authentication. Create jwt.go with JWT generation and validation. Modify auth.go to use JWT instead of sessions.", "agent": "coder", "mode": "implement"}`),
			runnertest.ChunkDone(),
		},
		// Turn 4: Verify and complete
		{
			runnertest.ChunkText("All workers completed. JWT refactor is done."),
			runnertest.ChunkDone(),
		},
	}

	return runnertest.NewClient(turns)
}

// SpawnScriptedClient creates a client for testing the spawn tool behavior directly.
// It tests that mode enforcement works correctly.
func SpawnScriptedClient(testCase string) runner.Client {
	switch testCase {
	case "explore_blocked":
		// Tests that explore mode cannot write files
		return runnertest.NewClient([][]llm.CompletionChunk{
			{
				runnertest.ChunkToolCall("c1", "spawn_agent", `{"prompt": "Try to write a file", "agent": "researcher", "mode": "explore"}`),
				runnertest.ChunkDone(),
			},
		})

	case "implement_allowed":
		// Tests that implement mode can write files
		return runnertest.NewClient([][]llm.CompletionChunk{
			{
				runnertest.ChunkToolCall("c1", "spawn_agent", `{"prompt": "Create jwt.go", "agent": "coder", "mode": "implement"}`),
				runnertest.ChunkDone(),
			},
		})

	default:
		return runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkText("done"), runnertest.ChunkDone()},
		})
	}
}

// Note: The spawn tool result is automatically shaped by the spawn tool itself.
// The scripted client just needs to emit the tool call; the spawn tool's Execute
// method will return the appropriate result based on the child agent's execution.
