package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type PromptID string

type Prompt struct {
	ID      PromptID
	Name    string
	Content string
	Active  bool
}

type PromptRepo struct {
	q *gen.Queries
}

func NewPromptRepo(q *gen.Queries) *PromptRepo {
	return &PromptRepo{q: q}
}

func (r *PromptRepo) GetActive(ctx context.Context) (Prompt, error) {
	row, err := r.q.GetActivePrompt(ctx)
	if err != nil {
		return Prompt{}, fmt.Errorf("get active prompt: %w", err)
	}
	return Prompt{
		ID:      PromptID(row.ID),
		Name:    row.Name,
		Content: row.Content,
		Active:  row.Active,
	}, nil
}

// GetActivePrompt returns the active prompt's (id, content) or an error. Matches
// the taskrunner.ActivePromptReader interface for prompt-update tooling.
func (r *PromptRepo) GetActivePrompt(ctx context.Context) (string, string, error) {
	p, err := r.GetActive(ctx)
	if err != nil {
		return "", "", err
	}
	return string(p.ID), p.Content, nil
}

func (r *PromptRepo) List(ctx context.Context) ([]Prompt, error) {
	rows, err := r.q.ListPrompts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	prompts := make([]Prompt, len(rows))
	for i, row := range rows {
		prompts[i] = Prompt{
			ID:      PromptID(row.ID),
			Name:    row.Name,
			Content: row.Content,
			Active:  row.Active,
		}
	}
	return prompts, nil
}

func (r *PromptRepo) Create(ctx context.Context, name, content string) (Prompt, error) {
	id := uuid.New().String()
	err := r.q.CreatePrompt(ctx, gen.CreatePromptParams{
		ID:      id,
		Name:    name,
		Content: content,
		Active:  false,
	})
	if err != nil {
		return Prompt{}, fmt.Errorf("create prompt: %w", err)
	}
	return Prompt{ID: PromptID(id), Name: name, Content: content, Active: false}, nil
}

func (r *PromptRepo) UpdateContent(ctx context.Context, id PromptID, content string) error {
	err := r.q.UpdatePromptContent(ctx, gen.UpdatePromptContentParams{
		Content: content,
		ID:      string(id),
	})
	if err != nil {
		return fmt.Errorf("update prompt: %w", err)
	}
	return nil
}

func (r *PromptRepo) SetActive(ctx context.Context, id PromptID) error {
	err := r.q.SetPromptActive(ctx, string(id))
	if err != nil {
		return fmt.Errorf("set active: %w", err)
	}
	return nil
}

func (r *PromptRepo) Delete(ctx context.Context, id PromptID) error {
	err := r.q.DeletePrompt(ctx, string(id))
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	return nil
}
