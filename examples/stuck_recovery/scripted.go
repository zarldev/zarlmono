package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// NewScriptedClient creates a deterministic client that simulates
// an agent repeatedly failing to find a non-existent function.
// This triggers the DecomposeGuardrail's graduated response.
func NewScriptedClient() runner.Client {
	// The script simulates an agent that keeps retrying the IDENTICAL
	// failing search — same (tool, args) signature each time — so the
	// DecomposeGuardrail's per-signature counter escalates:
	// 1-2: Normal failures (pass through)
	// 3: Failure with advisory from guardrail (nudge)
	// 4: Failure triggers fatal (blocked)
	// 5: Recovery via spawn_agent
	// The pattern is kept identical on turns 1-4 on purpose: distinct
	// patterns would each be a fresh signature and never trip the
	// signature escalation this example is demonstrating.
	turns := [][]llm.CompletionChunk{
		// Turn 1: Search for the function (failure 1 — pass through).
		{
			runnertest.ChunkToolCall("c1", string(ToolGrep), `{"pattern": "NonExistentHandler"}`),
			runnertest.ChunkDone(),
		},
		// Turn 2: Retry the same call (failure 2 — pass through).
		{
			runnertest.ChunkToolCall("c2", string(ToolGrep), `{"pattern": "NonExistentHandler"}`),
			runnertest.ChunkDone(),
		},
		// Turn 3: Retry again (failure 3 — triggers advisory).
		{
			runnertest.ChunkToolCall("c3", string(ToolGrep), `{"pattern": "NonExistentHandler"}`),
			runnertest.ChunkDone(),
		},
		// Turn 4: Retry once more (failure 4 — triggers fatal).
		{
			runnertest.ChunkToolCall("c4", string(ToolGrep), `{"pattern": "NonExistentHandler"}`),
			runnertest.ChunkDone(),
		},
		// Turn 5: Spawn a researcher
		{
			runnertest.ChunkToolCall("c5", string(ToolSpawn), `{"prompt": "Search the codebase thoroughly for any function related to handler. List all files and search for handler-related patterns.", "agent": "researcher", "mode": "explore"}`),
			runnertest.ChunkDone(),
		},
		// Turn 6: Conclude from the researcher's findings (no tool calls
		// ends the run; the oracle then verifies against the filesystem).
		{
			runnertest.ChunkText("Confirmed: NonExistentHandler does not exist in the codebase."),
			runnertest.ChunkDone(),
		},
	}

	return runnertest.NewClient(turns)
}
