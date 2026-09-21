package tui

import (
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// executionHistoryRow owns the complete canonical occurrence, not a latest-call
// projection. Position distinguishes legacy occurrences without execution IDs.
type executionHistoryRow struct {
	output      runner.ToolOutput
	position    int
	interrupted bool
}

func (r executionHistoryRow) details(width int) []string {
	o := r.output
	lines := []string{"Scope: root tasks and nested executions; delegated private history excluded",
		fmt.Sprintf("Replay position: %d", r.position)}
	if o.ExecutionID != "" {
		lines = append(lines, "Execution: "+o.ExecutionID)
	}
	if o.TaskID != "" {
		lines = append(lines, fmt.Sprintf("Task: %s · attempt %d", o.TaskID, o.Attempt))
	}
	if o.ParentExecutionID != "" {
		lines = append(lines, fmt.Sprintf("Parent execution: %s · sequence %d", o.ParentExecutionID, o.Sequence))
	}
	disposition := "unknown (legacy)"
	if o.Dispatched != nil {
		disposition = "not dispatched"
		if *o.Dispatched {
			disposition = "executor invoked"
		}
	}
	lines = append(lines, "Dispatch: "+disposition)
	if r.interrupted {
		lines = append(lines, "Interrupted; excluded from model context")
	}
	if !o.Success {
		lines = append(lines, "Failure kind: "+o.Kind.String())
	}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"Decoded parameters", o.Parameters}, {"Attachments", o.Parts}, {"Effects", o.Effects},
	} {
		data, err := json.MarshalIndent(field.value, "", "  ")
		if err == nil && string(data) != "null" {
			lines = append(lines, field.name+":", string(data))
		}
	}
	var out []string
	for _, line := range lines {
		out = append(out, renderPlain(width, line, withStyle(palette.Subtle.On))...)
	}
	return out
}
