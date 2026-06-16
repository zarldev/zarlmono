package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// NewScriptedClient simulates a long research task that triggers compaction.
// The agent reads multiple large files, causing context pressure.
func NewScriptedClient() runner.Client {
	// Simulates:
	// 1. list files
	// 2. read docs.go (verbose)
	// 3. read utils.go (verbose)
	// 4. read handlers.go (verbose)
	// 5. push docs for docs.go
	// 6. push docs for utils.go
	// 7. push docs for handlers.go
	turns := [][]llm.CompletionChunk{
		// Turn 1: List files
		{
			runnertest.ChunkToolCall("c1", string(ToolListFiles), `{}`),
			runnertest.ChunkDone(),
		},
		// Turn 2: Read docs.go
		{
			runnertest.ChunkToolCall("c2", string(ToolReadFile), `{"path": "docs.go"}`),
			runnertest.ChunkDone(),
		},
		// Turn 3: Read utils.go
		{
			runnertest.ChunkToolCall("c3", string(ToolReadFile), `{"path": "utils.go"}`),
			runnertest.ChunkDone(),
		},
		// Turn 4: Read handlers.go
		{
			runnertest.ChunkToolCall("c4", string(ToolReadFile), `{"path": "handlers.go"}`),
			runnertest.ChunkDone(),
		},
		// Turn 5: Push docs for docs.go
		{
			runnertest.ChunkToolCall("c5", string(ToolPushDocs), `{"title": "API Documentation", "content": "## Overview\n\nAPI provides CRUD operations for user resources.\n\n## Endpoints\n\n- GET /users - List all users\n- GET /users/{id} - Get specific user\n\n## Auth\n\nBearer token required."}`),
			runnertest.ChunkDone(),
		},
		// Turn 6: Push docs for utils.go
		{
			runnertest.ChunkToolCall("c6", string(ToolPushDocs), `{"title": "Utility Functions", "content": "## Functions\n\n- extractID: Extracts user ID from URL\n- decodeJSON: Decodes request body\n- writeJSON: Writes JSON response\n- ValidateEmail: Validates email format\n- ValidateName: Validates name format\n- ValidateRole: Validates user role"}`),
			runnertest.ChunkDone(),
		},
		// Turn 7: Push docs for handlers.go
		{
			runnertest.ChunkToolCall("c7", string(ToolPushDocs), `{"title": "HTTP Handlers", "content": "## Handlers\n\n- GetUserHandler: GET /users/{id}\n- CreateUserHandler: POST /users\n- UpdateUserHandler: PUT /users/{id}\n- DeleteUserHandler: DELETE /users/{id}\n- ListUsersHandler: GET /users\n\n## Request Types\n\n- CreateUserRequest: email, name, role, metadata\n- UpdateUserRequest: email, name, role, metadata"}`),
			runnertest.ChunkDone(),
		},
		// Turn 8: Final assistant message — no tool call — so the runner
		// terminates cleanly (TerminalCompleted) once all 3 docs are pushed,
		// rather than being asked for an 8th turn it doesn't have.
		{
			runnertest.ChunkText("All three files are now documented."),
			runnertest.ChunkDone(),
		},
	}

	return runnertest.NewClient(turns)
}
