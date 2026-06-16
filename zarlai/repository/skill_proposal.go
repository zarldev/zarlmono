package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// SkillProposal is the LLM-authored request to create or update a skill.
// Pending until a human reviews; on approval the admin layer applies
// the change to the skills table.
type SkillProposal struct {
	ID                  string
	TargetSkillID       *string
	ProposedName        string
	ProposedDescription string
	ProposedMarkdown    string
	ProposedBinding     *string
	Rationale           string
	Status              string // pending | approved | rejected
	CreatedAt           time.Time
	ReviewedAt          *time.Time
}

type SkillProposalRepo struct {
	q *gen.Queries
}

func NewSkillProposalRepo(q *gen.Queries) *SkillProposalRepo {
	return &SkillProposalRepo{q: q}
}

func (r *SkillProposalRepo) List(ctx context.Context) ([]SkillProposal, error) {
	rows, err := r.q.ListSkillProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skill proposals: %w", err)
	}
	out := make([]SkillProposal, len(rows))
	for i, row := range rows {
		out[i] = skillProposalFromRow(row.ID, row.TargetSkillID, row.ProposedName,
			row.ProposedDescription, row.ProposedMarkdown, row.ProposedBinding,
			row.Rationale, row.Status, row.CreatedAt, row.ReviewedAt)
	}
	return out, nil
}

func (r *SkillProposalRepo) Get(ctx context.Context, id string) (SkillProposal, error) {
	row, err := r.q.GetSkillProposal(ctx, id)
	if err != nil {
		return SkillProposal{}, fmt.Errorf("get skill proposal %q: %w", id, err)
	}
	return skillProposalFromRow(row.ID, row.TargetSkillID, row.ProposedName,
		row.ProposedDescription, row.ProposedMarkdown, row.ProposedBinding,
		row.Rationale, row.Status, row.CreatedAt, row.ReviewedAt), nil
}

// Create inserts a pending proposal. Generates a new UUID.
func (r *SkillProposalRepo) Create(ctx context.Context, p SkillProposal) (SkillProposal, error) {
	if p.ProposedName == "" || p.ProposedMarkdown == "" {
		return SkillProposal{}, errors.New("create skill proposal: name and markdown are required")
	}
	p.ID = uuid.NewString()
	if err := r.q.CreateSkillProposal(ctx, gen.CreateSkillProposalParams{
		ID:                  p.ID,
		TargetSkillID:       nullString(p.TargetSkillID),
		ProposedName:        p.ProposedName,
		ProposedDescription: p.ProposedDescription,
		ProposedMarkdown:    p.ProposedMarkdown,
		ProposedBinding:     nullString(p.ProposedBinding),
		Rationale:           p.Rationale,
	}); err != nil {
		return SkillProposal{}, fmt.Errorf("create skill proposal: %w", err)
	}
	return r.Get(ctx, p.ID)
}

// SetStatus transitions a proposal to approved or rejected and stamps
// the review time. Idempotent for the same target status.
func (r *SkillProposalRepo) SetStatus(ctx context.Context, id, status string) error {
	if err := r.q.SetSkillProposalStatus(ctx, gen.SetSkillProposalStatusParams{
		ID:     id,
		Status: status,
	}); err != nil {
		return fmt.Errorf("set skill proposal %q status: %w", id, err)
	}
	return nil
}

func skillProposalFromRow(
	id string, target sql.NullString, name, description, markdown string,
	binding sql.NullString, rationale, status string,
	createdAt time.Time, reviewedAt sql.NullTime,
) SkillProposal {
	var reviewed *time.Time
	if reviewedAt.Valid {
		t := reviewedAt.Time
		reviewed = &t
	}
	return SkillProposal{
		ID:                  id,
		TargetSkillID:       nullableString(target),
		ProposedName:        name,
		ProposedDescription: description,
		ProposedMarkdown:    markdown,
		ProposedBinding:     nullableString(binding),
		Rationale:           rationale,
		Status:              status,
		CreatedAt:           createdAt,
		ReviewedAt:          reviewed,
	}
}
