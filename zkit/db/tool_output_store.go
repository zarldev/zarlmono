package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// ToolOutputRecord is the store's view of one captured, untruncated tool
// result, keyed to a session and the tool call that produced it.
type ToolOutputRecord struct {
	ID         int64
	SessionID  string
	ToolCallID string
	ToolName   string
	ArgsJSON   string
	Output     string
	CreatedAt  time.Time
}

// SaveToolOutput upserts a captured tool result. CreatedAt is preserved
// (caller-owned metadata); a zero CreatedAt is replaced with time.Now().
func (s *Store) SaveToolOutput(ctx context.Context, sessionID string, r ToolOutputRecord) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	err := s.q.InsertToolOutput(ctx, gen.InsertToolOutputParams{
		SessionID:  sessionID,
		ToolCallID: r.ToolCallID,
		ToolName:   r.ToolName,
		ArgsJson:   r.ArgsJSON,
		Output:     r.Output,
		CreatedAt:  r.CreatedAt.Unix(),
	})
	if err != nil {
		return fmt.Errorf("save tool output %q: %w", r.ToolCallID, err)
	}
	return nil
}

// ListToolOutputsBySession returns the captured tool results for a session,
// oldest first. Empty slice (not nil) when none are stored.
func (s *Store) ListToolOutputsBySession(ctx context.Context, sessionID string) ([]ToolOutputRecord, error) {
	rows, err := s.q.ListToolOutputsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tool outputs for %q: %w", sessionID, err)
	}
	out := make([]ToolOutputRecord, len(rows))
	for i, r := range rows {
		out[i] = toToolOutputRecord(r)
	}
	return out, nil
}

// GetToolOutput fetches one captured tool result by call id. Returns
// [ErrNotFound] when the row is absent so callers can branch without
// importing database/sql.
func (s *Store) GetToolOutput(ctx context.Context, sessionID, toolCallID string) (ToolOutputRecord, error) {
	row, err := s.q.GetToolOutput(ctx, gen.GetToolOutputParams{
		SessionID:  sessionID,
		ToolCallID: toolCallID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ToolOutputRecord{}, ErrNotFound
		}
		return ToolOutputRecord{}, fmt.Errorf("get tool output %q: %w", toolCallID, err)
	}
	return toToolOutputRecord(row), nil
}

func toToolOutputRecord(r gen.ToolOutput) ToolOutputRecord {
	return ToolOutputRecord{
		ID:         r.ID,
		SessionID:  r.SessionID,
		ToolCallID: r.ToolCallID,
		ToolName:   r.ToolName,
		ArgsJSON:   r.ArgsJson,
		Output:     r.Output,
		CreatedAt:  time.Unix(r.CreatedAt, 0),
	}
}

// ToolOutputSummary is the metadata view of a captured tool result, without
// the (potentially large) output body.
type ToolOutputSummary struct {
	ToolCallID string
	ToolName   string
	ArgsJSON   string
	CreatedAt  time.Time
}

// ListToolOutputSummariesBySession returns the captured tool results for a
// session — metadata only, no output bodies — oldest first. Use GetToolOutput
// to load a single result's full output on demand.
func (s *Store) ListToolOutputSummariesBySession(ctx context.Context, sessionID string) ([]ToolOutputSummary, error) {
	rows, err := s.q.ListToolOutputSummariesBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tool output summaries for %q: %w", sessionID, err)
	}
	out := make([]ToolOutputSummary, len(rows))
	for i, r := range rows {
		out[i] = ToolOutputSummary{
			ToolCallID: r.ToolCallID,
			ToolName:   r.ToolName,
			ArgsJSON:   r.ArgsJson,
			CreatedAt:  time.Unix(r.CreatedAt, 0),
		}
	}
	return out, nil
}
