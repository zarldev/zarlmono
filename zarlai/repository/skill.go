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

// Skill is the domain-level shape of a row in the skills table.
//
// ProfileBinding is nil when the skill applies globally across every
// profile, or set to a profile name ("default" / "researcher" / "coder")
// when scoped. Enabled lets operators temporarily silence a skill
// without deleting it — useful while iterating on wording.
type Skill struct {
	ID             string
	Name           string
	Description    string
	Markdown       string
	ProfileBinding *string
	Enabled        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SkillRepo struct {
	q *gen.Queries
}

func NewSkillRepo(q *gen.Queries) *SkillRepo {
	return &SkillRepo{q: q}
}

func (r *SkillRepo) List(ctx context.Context) ([]Skill, error) {
	rows, err := r.q.ListSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	out := make([]Skill, len(rows))
	for i, row := range rows {
		out[i] = Skill{
			ID:             row.ID,
			Name:           row.Name,
			Description:    row.Description,
			Markdown:       row.Markdown,
			ProfileBinding: nullableString(row.ProfileBinding),
			Enabled:        row.Enabled,
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
		}
	}
	return out, nil
}

// ListEnabled returns only skills where enabled = TRUE. Used by the
// in-memory loader at startup so disabled skills never enter the
// prompt-injection path.
func (r *SkillRepo) ListEnabled(ctx context.Context) ([]Skill, error) {
	rows, err := r.q.ListEnabledSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled skills: %w", err)
	}
	out := make([]Skill, len(rows))
	for i, row := range rows {
		out[i] = Skill{
			ID:             row.ID,
			Name:           row.Name,
			Description:    row.Description,
			Markdown:       row.Markdown,
			ProfileBinding: nullableString(row.ProfileBinding),
			Enabled:        row.Enabled,
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
		}
	}
	return out, nil
}

func (r *SkillRepo) Get(ctx context.Context, id string) (Skill, error) {
	row, err := r.q.GetSkill(ctx, id)
	if err != nil {
		return Skill{}, fmt.Errorf("get skill %q: %w", id, err)
	}
	return Skill{
		ID:             row.ID,
		Name:           row.Name,
		Description:    row.Description,
		Markdown:       row.Markdown,
		ProfileBinding: nullableString(row.ProfileBinding),
		Enabled:        row.Enabled,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

// Create generates a new UUID for the skill and inserts it.
func (r *SkillRepo) Create(ctx context.Context, s Skill) (Skill, error) {
	if s.Name == "" {
		return Skill{}, errors.New("create skill: name is required")
	}
	s.ID = uuid.NewString()
	if err := r.q.CreateSkill(ctx, gen.CreateSkillParams{
		ID:             s.ID,
		Name:           s.Name,
		Description:    s.Description,
		Markdown:       s.Markdown,
		ProfileBinding: nullString(s.ProfileBinding),
		Enabled:        s.Enabled,
	}); err != nil {
		return Skill{}, fmt.Errorf("create skill %q: %w", s.Name, err)
	}
	return r.Get(ctx, s.ID)
}

func (r *SkillRepo) Update(ctx context.Context, s Skill) error {
	if s.ID == "" {
		return errors.New("update skill: id is required")
	}
	if err := r.q.UpdateSkill(ctx, gen.UpdateSkillParams{
		ID:             s.ID,
		Name:           s.Name,
		Description:    s.Description,
		Markdown:       s.Markdown,
		ProfileBinding: nullString(s.ProfileBinding),
		Enabled:        s.Enabled,
	}); err != nil {
		return fmt.Errorf("update skill %q: %w", s.ID, err)
	}
	return nil
}

func (r *SkillRepo) Delete(ctx context.Context, id string) error {
	if err := r.q.DeleteSkill(ctx, id); err != nil {
		return fmt.Errorf("delete skill %q: %w", id, err)
	}
	return nil
}

// nullString wraps *string into the sql.NullString sqlc expects for
// nullable TEXT columns. A nil pointer produces Valid=false (writes
// NULL).
func nullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// nullableString is the inverse — sql.NullString → *string with nil for
// NULL — so the domain type uses plain pointers.
func nullableString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	out := s.String
	return &out
}
