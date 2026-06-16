package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func defaultScript() [][]llm.CompletionChunk {
	return [][]llm.CompletionChunk{
		// 1-3. Check each endpoint. Unknown → healthy on first check.
		{runnertest.ChunkToolCall("c1", ToolCheckEndpoint.String(), `{"name":"api"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c2", ToolCheckEndpoint.String(), `{"name":"db"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c3", ToolCheckEndpoint.String(), `{"name":"cache"}`), runnertest.ChunkDone()},
		// 4. All healthy — settle.
		{runnertest.ChunkText("all endpoints healthy"), runnertest.ChunkDone()},
	}
}
