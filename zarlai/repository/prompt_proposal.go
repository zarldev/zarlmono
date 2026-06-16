package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type PromptProposal struct {
	ID              string
	CurrentPromptID string
	ProposedContent string
	Rationale       string
	Status          string
	CreatedAt       string
}

type PromptProposalRepo struct {
	q *gen.Queries
}

func NewPromptProposalRepo(q *gen.Queries) *PromptProposalRepo {
	return &PromptProposalRepo{q: q}
}

func (r *PromptProposalRepo) Create(ctx context.Context, currentPromptID, proposedContent, rationale string) (PromptProposal, error) {
	id := uuid.New().String()
	err := r.q.InsertPromptProposal(ctx, gen.InsertPromptProposalParams{
		ID:              id,
		CurrentPromptID: currentPromptID,
		ProposedContent: proposedContent,
		Rationale:       rationale,
	})
	if err != nil {
		return PromptProposal{}, fmt.Errorf("insert prompt proposal: %w", err)
	}
	return PromptProposal{
		ID:              id,
		CurrentPromptID: currentPromptID,
		ProposedContent: proposedContent,
		Rationale:       rationale,
		Status:          "pending",
	}, nil
}

func (r *PromptProposalRepo) List(ctx context.Context) ([]PromptProposal, error) {
	rows, err := r.q.ListPromptProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list prompt proposals: %w", err)
	}
	out := make([]PromptProposal, len(rows))
	for i, row := range rows {
		out[i] = PromptProposal{
			ID:              row.ID,
			CurrentPromptID: row.CurrentPromptID,
			ProposedContent: row.ProposedContent,
			Rationale:       row.Rationale,
			Status:          row.Status,
			CreatedAt:       row.CreatedAt.Format(timeFormat),
		}
	}
	return out, nil
}

func (r *PromptProposalRepo) Get(ctx context.Context, id string) (PromptProposal, error) {
	row, err := r.q.GetPromptProposal(ctx, id)
	if err != nil {
		return PromptProposal{}, fmt.Errorf("get prompt proposal: %w", err)
	}
	return PromptProposal{
		ID:              row.ID,
		CurrentPromptID: row.CurrentPromptID,
		ProposedContent: row.ProposedContent,
		Rationale:       row.Rationale,
		Status:          row.Status,
		CreatedAt:       row.CreatedAt.Format(timeFormat),
	}, nil
}

func (r *PromptProposalRepo) SetStatus(ctx context.Context, id, status string) error {
	return r.q.UpdatePromptProposalStatus(ctx, gen.UpdatePromptProposalStatusParams{
		Status: status,
		ID:     id,
	})
}

func (r *PromptProposalRepo) CountPending(ctx context.Context) (int64, error) {
	return r.q.CountPendingPromptProposals(ctx)
}

// CreatePromptProposal satisfies the taskrunner.PromptProposalStore interface.
func (r *PromptProposalRepo) CreatePromptProposal(ctx context.Context, currentPromptID, proposedContent, rationale string) error {
	_, err := r.Create(ctx, currentPromptID, proposedContent, rationale)
	return err
}
