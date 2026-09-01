package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// NewScriptedClient creates a deterministic client that simulates the model
// calling agent_spawn with different workers in sequence.
func NewScriptedClient(fs *FileSystem) runner.Client {
	_ = fs // reserved for future use — may validate script against filesystem state
	turns := [][]llm.CompletionChunk{
		// Parent delegates the refactor to the coder.
		{
			runnertest.ChunkToolCall("p1", "agent_spawn", `{"prompt": "Create jwt.go and switch auth.go to JWT.", "agent": "coder", "mode": "implement"}`),
		},
		// The child creates jwt.go.
		{
			runnertest.ChunkToolCall("c1", string(ToolWriteFile), `{"path": "jwt.go", "content": "package auth\n\nfunc ValidateJWT(t string) bool { return true }"}`),
		},
		// The child rewrites auth.go to use JWT.
		{
			runnertest.ChunkToolCall("c2", string(ToolWriteFile), `{"path": "auth.go", "content": "package auth\n\n// Authentication now uses JWT."}`),
		},
		// The child reports completion, then the parent finishes.
		{runnertest.ChunkText("Created jwt.go and migrated auth.go to JWT.")},
		{runnertest.ChunkText("Refactor complete.")},
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
				runnertest.ChunkToolCall("c1", "agent_spawn", `{"prompt": "Try to write a file", "agent": "researcher", "mode": "explore"}`),
			},
		})

	case "implement_allowed":
		// Tests that implement mode can write files
		return runnertest.NewClient([][]llm.CompletionChunk{
			{
				runnertest.ChunkToolCall("c1", "agent_spawn", `{"prompt": "Create jwt.go", "agent": "coder", "mode": "implement"}`),
			},
		})

	default:
		return runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkText("done")},
		})
	}
}

// Note: The spawn tool result is automatically shaped by the spawn tool itself.
// The scripted client just needs to emit the tool call; the spawn tool's Execute
// method will return the appropriate result based on the child agent's execution.
