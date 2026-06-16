package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type ConversationSummary struct {
	ID         string
	PersonName string
	Summary    string
	SessionID  string
	CreatedAt  string
}

type ConversationSummaryRepo struct {
	q *gen.Queries
}

func NewConversationSummaryRepo(q *gen.Queries) *ConversationSummaryRepo {
	return &ConversationSummaryRepo{q: q}
}

func (r *ConversationSummaryRepo) Create(ctx context.Context, personName, summary, sessionID string) (ConversationSummary, error) {
	id := uuid.New().String()
	err := r.q.InsertConversationSummary(ctx, gen.InsertConversationSummaryParams{
		ID:         id,
		PersonName: personName,
		Summary:    summary,
		SessionID:  sessionID,
	})
	if err != nil {
		return ConversationSummary{}, fmt.Errorf("insert conversation summary: %w", err)
	}
	return ConversationSummary{
		ID:         id,
		PersonName: personName,
		Summary:    summary,
		SessionID:  sessionID,
	}, nil
}

func (r *ConversationSummaryRepo) ListRecent(ctx context.Context, personName string, limit int) ([]ConversationSummary, error) {
	rows, err := r.q.ListRecentSummariesByPerson(ctx, gen.ListRecentSummariesByPersonParams{
		PersonName: personName,
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list recent summaries: %w", err)
	}
	out := make([]ConversationSummary, len(rows))
	for i, row := range rows {
		out[i] = ConversationSummary{
			ID:         row.ID,
			PersonName: row.PersonName,
			Summary:    row.Summary,
			SessionID:  row.SessionID,
			CreatedAt:  row.CreatedAt.Format(timeFormat),
		}
	}
	return out, nil
}

// ListPaged returns conversation summaries across every person, newest
// first. When personName is non-empty, only that person's summaries are
// returned. Used by the admin chat-history tab.
func (r *ConversationSummaryRepo) ListPaged(ctx context.Context, personName string, limit, offset int) ([]ConversationSummary, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []gen.ConversationSummary
	var total int64
	var err error
	if personName == "" {
		rows, err = r.q.ListConversationSummariesPaged(ctx, gen.ListConversationSummariesPagedParams{
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			return nil, 0, fmt.Errorf("list summaries: %w", err)
		}
		total, err = r.q.CountConversationSummaries(ctx)
	} else {
		rows, err = r.q.ListConversationSummariesPagedByPerson(ctx, gen.ListConversationSummariesPagedByPersonParams{
			PersonName: personName,
			Limit:      int32(limit),
			Offset:     int32(offset),
		})
		if err != nil {
			return nil, 0, fmt.Errorf("list summaries for %s: %w", personName, err)
		}
		total, err = r.q.CountConversationSummariesByPerson(ctx, personName)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("count summaries: %w", err)
	}
	out := make([]ConversationSummary, len(rows))
	for i, row := range rows {
		out[i] = ConversationSummary{
			ID:         row.ID,
			PersonName: row.PersonName,
			Summary:    row.Summary,
			SessionID:  row.SessionID,
			CreatedAt:  row.CreatedAt.Format(timeFormat),
		}
	}
	return out, total, nil
}

func (r *ConversationSummaryRepo) Delete(ctx context.Context, id string) error {
	err := r.q.DeleteConversationSummary(ctx, id)
	if err != nil {
		return fmt.Errorf("delete conversation summary: %w", err)
	}
	return nil
}

// DeleteAll removes every row. Used by the agent reset flow.
func (r *ConversationSummaryRepo) DeleteAll(ctx context.Context) (int64, error) {
	n, err := r.q.DeleteAllConversationSummaries(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete all conversation summaries: %w", err)
	}
	return n, nil
}
