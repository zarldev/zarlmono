package tui

import (
	"context"
	"errors"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/db"
)

// loadReplay reads committed occurrences from the session's own head, including
// shared ancestry and its independent suffix. It needs neither source sessions
// nor retained checkpoints. Only sessions without canonical history may fall back
// to the legacy latest-per-call projection.
func (h *toolHistory) loadReplay(ctx context.Context) bool {
	replay, err := h.store.ReadSessionReplay(ctx, h.sessionID)
	if errors.Is(err, db.ErrNotFound) {
		return false
	}
	if err != nil {
		h.status = "tool history: " + err.Error()
		return true
	}
	for i := len(replay) - 1; i >= 0; i-- {
		occurrence, err := runner.UnmarshalReplayMessage(replay[i])
		if err != nil {
			h.status = "tool history: invalid recorded occurrence"
			h.summaries = nil
			clear(h.full)
			clear(h.executions)
			return true
		}
		if occurrence.Tool == nil {
			continue
		}
		tool := occurrence.Tool
		index := len(h.summaries)
		h.executions[index] = executionHistoryRow{output: *tool, position: i + 1, interrupted: occurrence.Interrupted}
		h.summaries = append(h.summaries, db.ToolOutputSummary{ToolCallID: tool.ToolCallID, ToolName: tool.ToolName, ArgsJSON: tool.Args})
		// Recorded replay has no wall-clock field; do not invent execution times.
		output := tool.Output
		if tool.Error != "" {
			output = "Error: " + tool.Error + "\n" + output
		}
		if occurrence.Interrupted {
			output = "Interrupted observation (not admitted to model context)\n" + output
		}
		h.full[index] = db.ToolOutputRecord{SessionID: h.sessionID, ToolCallID: tool.ToolCallID, ToolName: tool.ToolName, ArgsJSON: tool.Args, Output: output}
	}
	return true
}
