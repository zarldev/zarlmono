package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
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
// Capture errors are returned to the runner instead of silently losing history.
type ToolOutputSink struct {
	Store     *db.Store
	SessionID func() string

	mu               sync.Mutex
	processSessionID map[string]string
}

var _ runner.ToolOutputSink = (*ToolOutputSink)(nil)

// Record implements runner.ToolOutputSink.
func (s *ToolOutputSink) Record(ctx context.Context, out runner.ToolOutput) error {
	if s == nil || s.Store == nil {
		return nil
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
		return nil
	}
	return s.recordForSession(ctx, sessionID, out)
}

func (s *ToolOutputSink) recordForSession(ctx context.Context, sessionID string, out runner.ToolOutput) error {
	if sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	parts, err := json.Marshal(out.Parts)
	if err != nil {
		return fmt.Errorf("encode tool history parts: %w", err)
	}
	parameters, err := json.Marshal(out.Parameters)
	if err != nil {
		return fmt.Errorf("encode tool history parameters: %w", err)
	}
	effects, err := json.Marshal(out.Effects)
	if err != nil {
		return fmt.Errorf("encode tool history effects: %w", err)
	}
	return s.Store.CaptureToolOutput(ctx, sessionID, db.ToolOutputHistory{
		ToolOutputRecord: db.ToolOutputRecord{ToolCallID: out.ToolCallID, ToolName: out.ToolName, ArgsJSON: out.Args, Output: out.Output},
		ExecutionID:      out.ExecutionID, ParentToolCallID: out.ParentToolCallID,
		ParentExecutionID: out.ParentExecutionID, TaskID: out.TaskID,
		Attempt: out.Attempt, Sequence: out.Sequence, Dispatched: out.Dispatched,
		ParametersJSON: string(parameters), PartsJSON: string(parts), EffectsJSON: string(effects),
		Success: out.Success, Error: out.Error, Kind: out.Kind,
	})
}

// RecordProcess adapts a background process's exit output to a ToolOutput
// record, keyed by the process ID.
func (s *ToolOutputSink) RecordProcess(ctx context.Context, id, command string, exitCode int, stdout, stderr []string) {
	if s == nil || s.Store == nil {
		return
	}
	output := strings.Join(append(stdout, stderr...), "\n")
	success := exitCode == 0
	terminalError := ""
	if !success {
		terminalError = fmt.Sprintf("process exited with code %d", exitCode)
	}
	sessionID := ""
	if s != nil {
		s.mu.Lock()
		sessionID = s.processSessionID[id]
		delete(s.processSessionID, id)
		s.mu.Unlock()
	}
	if err := s.recordForSession(ctx, sessionID, runner.ToolOutput{
		ToolCallID: id, ToolName: "bash", Args: command, Output: output,
		Success: success, Error: terminalError, Kind: tools.Kinds.UNKNOWN,
	}); err != nil {
		// Process-exit callbacks have no caller error channel. Report the storage
		// failure without copying raw command/output into ordinary diagnostics.
		slog.ErrorContext(ctx, "persist process history", "err", err)
	}
}
