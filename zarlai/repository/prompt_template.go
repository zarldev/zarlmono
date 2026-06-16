package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// PromptTemplate is a named operator-editable string used by code that
// assembles user-facing output. Examples: YAML frontmatter skeleton for
// research reports, report header line, task-runner boot prompt. The
// key is a stable identifier code looks up; the content is free-text
// with {{placeholders}} that callers substitute.
type PromptTemplate struct {
	Key       string
	Content   string
	UpdatedAt time.Time
}

type PromptTemplateRepo struct {
	q *gen.Queries
}

func NewPromptTemplateRepo(q *gen.Queries) *PromptTemplateRepo {
	return &PromptTemplateRepo{q: q}
}

func (r *PromptTemplateRepo) Get(ctx context.Context, key string) (PromptTemplate, error) {
	row, err := r.q.GetPromptTemplate(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PromptTemplate{}, err
		}
		return PromptTemplate{}, fmt.Errorf("get prompt template %q: %w", key, err)
	}
	return PromptTemplate{Key: row.TemplateKey, Content: row.Content, UpdatedAt: row.UpdatedAt}, nil
}

func (r *PromptTemplateRepo) List(ctx context.Context) ([]PromptTemplate, error) {
	rows, err := r.q.ListPromptTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list prompt templates: %w", err)
	}
	out := make([]PromptTemplate, len(rows))
	for i, row := range rows {
		out[i] = PromptTemplate{Key: row.TemplateKey, Content: row.Content, UpdatedAt: row.UpdatedAt}
	}
	return out, nil
}

func (r *PromptTemplateRepo) Upsert(ctx context.Context, key, content string) error {
	if key == "" {
		return errors.New("upsert prompt template: key is required")
	}
	if err := r.q.UpsertPromptTemplate(ctx, gen.UpsertPromptTemplateParams{
		TemplateKey: key,
		Content:     content,
	}); err != nil {
		return fmt.Errorf("upsert prompt template %q: %w", key, err)
	}
	return nil
}

func (r *PromptTemplateRepo) Delete(ctx context.Context, key string) error {
	if err := r.q.DeletePromptTemplate(ctx, key); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete prompt template %q: %w", key, err)
	}
	return nil
}
