package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// ToolOutputHistory is one immutable capture, distinct from the latest-per-call
// projection. ArgsJSON retains original text; ParametersJSON is decoded execution
// data and must never be presented as the original representation.
type ToolOutputHistory struct {
	ToolOutputRecord
	ExecutionID       string
	ParentToolCallID  string
	ParentExecutionID string
	TaskID            string
	Attempt           int
	Sequence          int
	Dispatched        *bool
	ParametersJSON    string
	PartsJSON         string
	EffectsJSON       string
	Success           bool
	Error             string
	Kind              tools.Kind
}

// CaptureToolOutput atomically appends raw history and updates the latest lookup.
// Reusing a provider call ID never replaces an earlier history entry.
func (s *Store) CaptureToolOutput(ctx context.Context, sessionID string, r ToolOutputHistory) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tool history: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	q := s.q.WithTx(tx)
	if err := q.AppendToolOutputHistory(ctx, gen.AppendToolOutputHistoryParams{
		SessionID: sessionID, ToolCallID: r.ToolCallID, ToolName: r.ToolName,
		ExecutionID: r.ExecutionID, ParentToolCallID: r.ParentToolCallID,
		ParentExecutionID: r.ParentExecutionID, TaskID: r.TaskID,
		Attempt: int64(r.Attempt), Sequence: int64(r.Sequence), Dispatched: nullableDispatch(r.Dispatched),
		ArgsJson: r.ArgsJSON, ParametersJson: r.ParametersJSON, Output: r.Output,
		PartsJson: r.PartsJSON, EffectsJson: r.EffectsJSON,
		Success: boolInt64(r.Success), Error: r.Error, Kind: r.Kind.String(),
		CreatedAt: r.CreatedAt.Unix(),
	}); err != nil {
		return fmt.Errorf("append tool history: %w", err)
	}
	if err := q.InsertToolOutput(ctx, gen.InsertToolOutputParams{
		SessionID: sessionID, ToolCallID: r.ToolCallID, ToolName: r.ToolName,
		ArgsJson: r.ArgsJSON, Output: r.Output, CreatedAt: r.CreatedAt.Unix(),
	}); err != nil {
		return fmt.Errorf("update tool output projection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tool history: %w", err)
	}
	return nil
}

// ListToolOutputHistory returns every new capture in append order, including
// repeated provider IDs. Pre-existing latest-output rows are not backfilled.
func (s *Store) ListToolOutputHistory(ctx context.Context, sessionID string) ([]ToolOutputHistory, error) {
	rows, err := s.read.ListToolOutputHistory(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tool history: %w", err)
	}
	out := make([]ToolOutputHistory, len(rows))
	for i, r := range rows {
		kind, err := tools.ParseKind(r.Kind)
		if err != nil {
			return nil, fmt.Errorf("parse tool history kind: %w", err)
		}
		out[i] = ToolOutputHistory{
			ToolOutputRecord: ToolOutputRecord{ID: r.ID, SessionID: r.SessionID, ToolCallID: r.ToolCallID, ToolName: r.ToolName, ArgsJSON: r.ArgsJson, Output: r.Output, CreatedAt: time.Unix(r.CreatedAt, 0)},
			ExecutionID:      r.ExecutionID, ParentToolCallID: r.ParentToolCallID,
			ParentExecutionID: r.ParentExecutionID, TaskID: r.TaskID,
			Attempt: int(r.Attempt), Sequence: int(r.Sequence),
			ParametersJSON: r.ParametersJson, PartsJSON: r.PartsJson, EffectsJSON: r.EffectsJson,
			Success: r.Success != 0, Error: r.Error, Kind: kind,
		}
		if r.Dispatched.Valid {
			out[i].Dispatched = new(r.Dispatched.Int64 != 0)
		}
	}
	return out, nil
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func nullableDispatch(value *bool) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: boolInt64(*value), Valid: true}
}
