package main

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// defaultScript returns the canned tool-call turns for -scripted mode.
// Each entry is one runner iteration: the tool call(s) the model would
// emit followed by a terminal chunk.  The script intentionally makes
// mistakes (early publish, missing field, weak notes) so the guardrails
// can demonstrate their corrective behavior.
func defaultScript() [][]llm.CompletionChunk {
	return [][]llm.CompletionChunk{
		// 1. The model tries the dangerous action first. release_ready blocks it.
		{runnertest.ChunkToolCall("c1", ToolPublish.String(), `{"channel":"production"}`), runnertest.ChunkDone()},
		// 2. The model supplies malformed JSON-shaped args. schema blocks it.
		{runnertest.ChunkToolCall("c2", ToolSetCheck.String(), `{"name":"tests","ok":true}`), runnertest.ChunkDone()},
		// 3. The model fixes the missing evidence field.
		{runnertest.ChunkToolCall("c3", ToolSetCheck.String(), `{"name":"tests","ok":true,"evidence":"go test ./... passed"}`), runnertest.ChunkDone()},
		// 4. A post-call guardrail rejects weak notes after the tool writes them.
		{runnertest.ChunkToolCall("c4", ToolWriteNotes.String(), `{"summary":"ok","risk":"low","rollback":"revert"}`), runnertest.ChunkDone()},
		// 5-6. Finish the release checklist.
		{runnertest.ChunkToolCall("c5", ToolSetCheck.String(), `{"name":"changelog","ok":true,"evidence":"CHANGELOG.md has v1.2.3 section"}`), runnertest.ChunkDone()},
		{runnertest.ChunkToolCall("c6", ToolSetCheck.String(), `{"name":"rollback_plan","ok":true,"evidence":"tag previous release and redeploy if needed"}`), runnertest.ChunkDone()},
		// 7. Good notes pass the post-call guardrail and become approved.
		{runnertest.ChunkToolCall("c7", ToolWriteNotes.String(), `{"summary":"Ships the guarded runner documentation example.","risk":"Low risk because it is documentation-only.","rollback":"Remove the docs page and keep the previous release."}`), runnertest.ChunkDone()},
		// 8. Now the pre-call release gate allows publish; the harness oracle stops.
		{runnertest.ChunkToolCall("c8", ToolPublish.String(), `{"channel":"production"}`), runnertest.ChunkDone()},
		// 9. If the watcher does not win the race against this fast scripted
		// runner, let the attempt complete naturally so the oracle can still
		// verify the published world state.
		{runnertest.ChunkText("release published to production"), runnertest.ChunkDone()},
	}
}
