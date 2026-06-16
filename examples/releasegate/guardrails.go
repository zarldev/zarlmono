package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// releaseReadyGuardrail is a PreCall rail: it blocks the side effectful publish
// tool until the release gate is green. The model sees the rejection as normal
// tool feedback and can call the status / check / notes tools to fix it.
type releaseReadyGuardrail struct{ r *Release }

func (g releaseReadyGuardrail) Name() string { return "release_ready" }

func (g releaseReadyGuardrail) Before(_ context.Context, call tools.ToolCall) error {
	if call.ToolName != ToolPublish {
		return nil
	}
	s := g.r.Snapshot()
	if len(s.Missing) == 0 {
		return nil
	}
	return tools.Validation(ToolPublish.String(), "release is not ready; missing: "+strings.Join(s.Missing, ", "))
}

// notesQualityGuardrail is a PostCall rail: the tool writes the proposed notes,
// then the guardrail inspects the effect and either approves it or rewrites the
// successful tool result into actionable feedback for the next model turn.
type notesQualityGuardrail struct{ r *Release }

func (g notesQualityGuardrail) Name() string { return "release_notes_quality" }

func successfulToolResult(result *tools.ToolResult, execErr error) bool {
	return execErr == nil && result != nil && result.Success
}

func (g notesQualityGuardrail) Inspect(_ context.Context, call tools.ToolCall, result *tools.ToolResult, execErr error) error {
	if call.ToolName != ToolWriteNotes || !successfulToolResult(result, execErr) {
		return nil
	}
	notes := g.r.Snapshot().Notes
	var missing []string
	if tooShort(notes.Summary) {
		missing = append(missing, "summary")
	}
	if tooShort(notes.Risk) {
		missing = append(missing, "risk")
	}
	if tooShort(notes.Rollback) {
		missing = append(missing, "rollback")
	}
	if len(missing) > 0 {
		return tools.Validation(ToolWriteNotes.String(), fmt.Sprintf(
			"release notes need concrete %s; rewrite notes with all three sections",
			strings.Join(missing, ", ")))
	}
	g.r.ApproveNotes()
	return nil
}

func tooShort(s string) bool { return len(strings.Fields(strings.TrimSpace(s))) < 3 }
