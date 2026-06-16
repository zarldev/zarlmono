package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// parseReasoningHistory maps the stored wire string to the enum, defaulting to
// INLINE for empty (pre-migration rows) or unrecognised values.
func parseReasoningHistory(s string) llm.ReasoningHistory {
	rh, err := llm.ParseReasoningHistory(s)
	if err != nil {
		return llm.ReasoningHistories.INLINE
	}
	return rh
}

// --- llm_providers ---
//
// *Store implements backends.Store directly (no adapter): the sqlite
// encoding — JSON seed lists, integer bools, timestamps — lives in
// these method bodies so the shared registry deals only in the neutral
// backends.StoredProvider type.

// ListProviders returns all custom provider rows. Conforms to
// backends.Store.
func (s *Store) ListProviders(ctx context.Context) ([]backends.StoredProvider, error) {
	rows, err := s.q.ListProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	out := make([]backends.StoredProvider, 0, len(rows))
	for _, r := range rows {
		var seeds []string
		if r.SeedModels != "" {
			_ = json.Unmarshal([]byte(r.SeedModels), &seeds)
		}
		out = append(out, backends.StoredProvider{
			Name:              r.Name,
			DisplayName:       r.DisplayName,
			AdapterType:       r.AdapterType,
			BaseURL:           r.BaseUrl,
			DefaultModel:      r.DefaultModel,
			SeedModels:        seeds,
			ReasoningHistory:  parseReasoningHistory(r.ReasoningHistory),
			ContextWindow:     int(r.ContextWindow),
			InputCostPerMTok:  r.InputCostPerMtok,
			OutputCostPerMTok: r.OutputCostPerMtok,
			Enabled:           r.Enabled != 0,
			Builtin:           r.Builtin != 0,
		})
	}
	return out, nil
}

// GetProvider returns a single provider by name, or ErrNotFound when absent.
func (s *Store) GetProvider(ctx context.Context, name string) (gen.LlmProvider, error) {
	p, err := s.q.GetProvider(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return gen.LlmProvider{}, ErrNotFound
		}
		return gen.LlmProvider{}, fmt.Errorf("get provider %q: %w", name, err)
	}
	return p, nil
}

// UpsertProvider inserts or replaces a custom provider. Conforms to
// backends.Store.
func (s *Store) UpsertProvider(ctx context.Context, p backends.StoredProvider) error {
	seedJSON := "[]"
	if len(p.SeedModels) > 0 {
		b, _ := json.Marshal(p.SeedModels)
		seedJSON = string(b)
	}
	now := time.Now().Unix()
	return s.q.UpsertProvider(ctx, gen.UpsertProviderParams{
		Name:              p.Name,
		DisplayName:       p.DisplayName,
		AdapterType:       p.AdapterType,
		BaseUrl:           p.BaseURL,
		ModelsUrl:         "",
		DefaultModel:      p.DefaultModel,
		SeedModels:        seedJSON,
		ReasoningHistory:  p.ReasoningHistory.String(),
		ContextWindow:     int64(p.ContextWindow),
		InputCostPerMtok:  p.InputCostPerMTok,
		OutputCostPerMtok: p.OutputCostPerMTok,
		Enabled:           boolToInt(p.Enabled),
		Builtin:           boolToInt(p.Builtin),
		CreatedAt:         now,
		UpdatedAt:         now,
	})
}

// DeleteProvider removes a provider row by name. Conforms to backends.Store.
func (s *Store) DeleteProvider(ctx context.Context, name string) error {
	return s.q.DeleteProvider(ctx, name)
}
