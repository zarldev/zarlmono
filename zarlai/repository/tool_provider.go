package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

type ToolProviderID string

type ToolProvider struct {
	ID      ToolProviderID
	Name    string
	Type    string
	Enabled bool
	Config  map[string]string
}

type ToolProviderRepo struct {
	q *gen.Queries
}

func NewToolProviderRepo(q *gen.Queries) *ToolProviderRepo {
	return &ToolProviderRepo{q: q}
}

func (r *ToolProviderRepo) List(ctx context.Context) ([]ToolProvider, error) {
	rows, err := r.q.ListToolProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tool providers: %w", err)
	}
	providers := make([]ToolProvider, len(rows))
	for i, row := range rows {
		p, err := toToolProvider(row)
		if err != nil {
			return nil, fmt.Errorf("list tool providers: %w", err)
		}
		providers[i] = p
	}
	return providers, nil
}

func (r *ToolProviderRepo) Get(ctx context.Context, name string) (ToolProvider, error) {
	row, err := r.q.GetToolProvider(ctx, name)
	if err != nil {
		return ToolProvider{}, fmt.Errorf("get tool provider: %w", err)
	}
	p, err := toToolProvider(row)
	if err != nil {
		return ToolProvider{}, fmt.Errorf("get tool provider: %w", err)
	}
	return p, nil
}

func (r *ToolProviderRepo) Create(ctx context.Context, name, providerType string, enabled bool, config map[string]string) (ToolProvider, error) {
	id := uuid.New().String()
	configJSON, err := json.Marshal(config)
	if err != nil {
		return ToolProvider{}, fmt.Errorf("create tool provider: %w", err)
	}
	err = r.q.CreateToolProvider(ctx, gen.CreateToolProviderParams{
		ID:      id,
		Name:    name,
		Type:    providerType,
		Enabled: enabled,
		Config:  configJSON,
	})
	if err != nil {
		return ToolProvider{}, fmt.Errorf("create tool provider: %w", err)
	}
	return ToolProvider{
		ID:      ToolProviderID(id),
		Name:    name,
		Type:    providerType,
		Enabled: enabled,
		Config:  config,
	}, nil
}

func (r *ToolProviderRepo) Update(ctx context.Context, id ToolProviderID, enabled bool, config map[string]string) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("update tool provider: %w", err)
	}
	err = r.q.UpdateToolProvider(ctx, gen.UpdateToolProviderParams{
		ID:      string(id),
		Enabled: enabled,
		Config:  configJSON,
	})
	if err != nil {
		return fmt.Errorf("update tool provider: %w", err)
	}
	return nil
}

func (r *ToolProviderRepo) Delete(ctx context.Context, id ToolProviderID) error {
	err := r.q.DeleteToolProvider(ctx, string(id))
	if err != nil {
		return fmt.Errorf("delete tool provider: %w", err)
	}
	return nil
}

func toToolProvider(row gen.ToolProvider) (ToolProvider, error) {
	var config map[string]string
	if err := json.Unmarshal(row.Config, &config); err != nil {
		return ToolProvider{}, fmt.Errorf("unmarshal config: %w", err)
	}
	return ToolProvider{
		ID:      ToolProviderID(row.ID),
		Name:    row.Name,
		Type:    row.Type,
		Enabled: row.Enabled,
		Config:  config,
	}, nil
}
