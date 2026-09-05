package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/db"
)

type toolOutputSessionKey struct{}

// WithToolOutputSession captures the session that owns outputs produced by a turn.
func WithToolOutputSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, toolOutputSessionKey{}, sessionID)
}

// ToolOutputSink persists full, untruncated tool results to the session's
// tool-output store. SessionID resolves the current session identity at record
// time, so the sink stays valid across -continue and new sessions; an empty
// identity means no durable session exists yet and the record is skipped.
// Capture is best-effort — a failed write is dropped rather than failing the
// turn.
type ToolOutputSink struct {
	Store     *db.Store
	SessionID func() string

	mu               sync.Mutex
	processSessionID map[string]string
}

var _ runner.ToolOutputSink = (*ToolOutputSink)(nil)

// Record implements runner.ToolOutputSink.
func (s *ToolOutputSink) Record(ctx context.Context, out runner.ToolOutput) {
	if s == nil || s.Store == nil {
		return
	}
	sessionID, _ := ctx.Value(toolOutputSessionKey{}).(string)
	if sessionID == "" && s.SessionID != nil {
		sessionID = s.SessionID()
	}
	for _, effect := range out.Effects {
		if effect.Process != nil && effect.Process.Background && effect.Process.ProcessID != "" {
			s.mu.Lock()
			if s.processSessionID == nil {
				s.processSessionID = make(map[string]string)
			}
			s.processSessionID[effect.Process.ProcessID] = sessionID
			s.mu.Unlock()
		}
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

func (s *ToolOutputSink) recordForSession(ctx context.Context, sessionID string, out runner.ToolOutput) {
	if sessionID == "" {
		return
	}
	_ = s.Store.SaveToolOutput(ctx, sessionID, db.ToolOutputRecord{
		ToolCallID: out.ToolCallID, ToolName: out.ToolName,
		ArgsJSON: out.Args, Output: out.Output,
	})
}

// RecordProcess adapts a background process's exit output to a ToolOutput
// record, keyed by the process ID.
func (s *ToolOutputSink) RecordProcess(ctx context.Context, id, command string, exitCode int, stdout, stderr []string) {
	if s == nil || s.Store == nil {
		return
	}
	output := strings.Join(append(stdout, stderr...), "\n")
	if exitCode != 0 {
		output += fmt.Sprintf("\n[exit %d]", exitCode)
	}
	sessionID := ""
	if s != nil {
		s.mu.Lock()
		sessionID = s.processSessionID[id]
		delete(s.processSessionID, id)
		s.mu.Unlock()
	}
	s.recordForSession(ctx, sessionID, runner.ToolOutput{
		ToolCallID: id, ToolName: "bash", Args: command, Output: output,
	})
}
