package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type ToolCall struct {
	ID         string
	SessionID  string
	ToolName   string
	Provider   string
	Args       string
	Result     string
	Error      string
	DurationMs int
	CreatedAt  string
}

type ToolCallStat struct {
	ToolName      string
	Provider      string
	TotalCalls    int
	AvgDurationMs float64
	ErrorCount    int
}

type ToolCallRepo struct {
	q *gen.Queries
}

func NewToolCallRepo(q *gen.Queries) *ToolCallRepo {
	return &ToolCallRepo{q: q}
}

func (r *ToolCallRepo) Log(ctx context.Context, tc ToolCall) error {
	if !json.Valid([]byte(tc.Args)) {
		return fmt.Errorf("log tool call: args is not valid JSON")
	}
	id := uuid.New().String()
	err := r.q.LogToolCall(ctx, gen.LogToolCallParams{
		ID:         id,
		SessionID:  tc.SessionID,
		ToolName:   tc.ToolName,
		Provider:   tc.Provider,
		Args:       json.RawMessage(tc.Args),
		Result:     tc.Result,
		Error:      tc.Error,
		DurationMs: int32(tc.DurationMs),
	})
	if err != nil {
		return fmt.Errorf("log tool call: %w", err)
	}
	return nil
}

func (r *ToolCallRepo) List(ctx context.Context, limit, offset int) ([]ToolCall, int, error) {
	rows, err := r.q.ListToolCalls(ctx, gen.ListToolCallsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list tool calls: %w", err)
	}
	total, err := r.q.CountToolCalls(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("list tool calls: %w", err)
	}
	calls := make([]ToolCall, len(rows))
	for i, row := range rows {
		calls[i] = ToolCall{
			ID:         row.ID,
			SessionID:  row.SessionID,
			ToolName:   row.ToolName,
			Provider:   row.Provider,
			Args:       string(row.Args),
			Result:     row.Result,
			Error:      row.Error,
			DurationMs: int(row.DurationMs),
			CreatedAt:  row.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}
	return calls, int(total), nil
}

func (r *ToolCallRepo) Stats(ctx context.Context) ([]ToolCallStat, error) {
	rows, err := r.q.ToolCallStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("tool call stats: %w", err)
	}
	stats := make([]ToolCallStat, len(rows))
	for i, row := range rows {
		stats[i] = ToolCallStat{
			ToolName:      row.ToolName,
			Provider:      row.Provider,
			TotalCalls:    int(row.TotalCalls),
			AvgDurationMs: parseInterfaceFloat(row.AvgDurationMs),
			ErrorCount:    int(parseInterfaceFloat(row.ErrorCount)),
		}
	}
	return stats, nil
}

// DeleteAll removes every row. Used by the agent reset flow.
func (r *ToolCallRepo) DeleteAll(ctx context.Context) (int64, error) {
	n, err := r.q.DeleteAllToolCalls(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete all tool calls: %w", err)
	}
	return n, nil
}

func parseInterfaceFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case []byte:
		f, err := strconv.ParseFloat(string(val), 64)
		if err != nil {
			return 0
		}
		return f
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}
