package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type ToolProposal struct {
	ID          string
	ToolName    string
	Description string
	MCPURL      string
	Rationale   string
	Status      string
	CreatedAt   string
}

type ToolProposalRepo struct {
	q *gen.Queries
}

func NewToolProposalRepo(q *gen.Queries) *ToolProposalRepo {
	return &ToolProposalRepo{q: q}
}

func (r *ToolProposalRepo) Create(ctx context.Context, toolName, description, mcpURL, rationale string) (ToolProposal, error) {
	id := uuid.New().String()
	err := r.q.InsertToolProposal(ctx, gen.InsertToolProposalParams{
		ID:          id,
		ToolName:    toolName,
		Description: description,
		McpUrl:      mcpURL,
		Rationale:   rationale,
	})
	if err != nil {
		return ToolProposal{}, fmt.Errorf("insert tool proposal: %w", err)
	}
	return ToolProposal{
		ID:          id,
		ToolName:    toolName,
		Description: description,
		MCPURL:      mcpURL,
		Rationale:   rationale,
		Status:      "pending",
	}, nil
}

func (r *ToolProposalRepo) List(ctx context.Context) ([]ToolProposal, error) {
	rows, err := r.q.ListToolProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tool proposals: %w", err)
	}
	out := make([]ToolProposal, len(rows))
	for i, row := range rows {
		out[i] = ToolProposal{
			ID:          row.ID,
			ToolName:    row.ToolName,
			Description: row.Description,
			MCPURL:      row.McpUrl,
			Rationale:   row.Rationale,
			Status:      row.Status,
			CreatedAt:   row.CreatedAt.Format(timeFormat),
		}
	}
	return out, nil
}

func (r *ToolProposalRepo) Get(ctx context.Context, id string) (ToolProposal, error) {
	row, err := r.q.GetToolProposal(ctx, id)
	if err != nil {
		return ToolProposal{}, fmt.Errorf("get tool proposal: %w", err)
	}
	return ToolProposal{
		ID:          row.ID,
		ToolName:    row.ToolName,
		Description: row.Description,
		MCPURL:      row.McpUrl,
		Rationale:   row.Rationale,
		Status:      row.Status,
		CreatedAt:   row.CreatedAt.Format(timeFormat),
	}, nil
}

func (r *ToolProposalRepo) SetStatus(ctx context.Context, id, status string) error {
	return r.q.UpdateToolProposalStatus(ctx, gen.UpdateToolProposalStatusParams{
		Status: status,
		ID:     id,
	})
}

// CreateProposal satisfies the taskrunner.ProposalStore interface.
func (r *ToolProposalRepo) CreateProposal(ctx context.Context, toolName, description, mcpURL, rationale string) error {
	_, err := r.Create(ctx, toolName, description, mcpURL, rationale)
	return err
}
