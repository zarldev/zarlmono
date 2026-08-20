package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/db"
)

// ToolOutputSink persists full, untruncated tool results to the session's
// tool-output store. SessionID resolves the current session identity at record
// time, so the sink stays valid across -continue and new sessions; an empty
// identity means no durable session exists yet and the record is skipped.
// Capture is best-effort — a failed write is dropped rather than failing the
// turn.
type ToolOutputSink struct {
	Store     *db.Store
	SessionID func() string
}

var _ runner.ToolOutputSink = (*ToolOutputSink)(nil)

// Record implements runner.ToolOutputSink.
func (s *ToolOutputSink) Record(ctx context.Context, out runner.ToolOutput) {
	if s == nil || s.Store == nil {
		return
	}
	sessionID := ""
	if s.SessionID != nil {
		sessionID = s.SessionID()
	}
	if sessionID == "" {
		return
	}
	_ = s.Store.SaveToolOutput(ctx, sessionID, db.ToolOutputRecord{
		ToolCallID: out.ToolCallID,
		ToolName:   out.ToolName,
		ArgsJSON:   out.Args,
		Output:     out.Output,
	})
}

// RecordProcess adapts a background process's exit output to a ToolOutput
// record, keyed by the process ID.
func (s *ToolOutputSink) RecordProcess(ctx context.Context, id, command string, exitCode int, stdout, stderr []string) {
	output := strings.Join(append(stdout, stderr...), "\n")
	if exitCode != 0 {
		output += fmt.Sprintf("\n[exit %d]", exitCode)
	}
	s.Record(ctx, runner.ToolOutput{
		ToolCallID: id,
		ToolName:   "bash",
		Args:       command,
		Output:     output,
	})
}
