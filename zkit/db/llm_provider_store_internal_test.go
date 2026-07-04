package db

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestLLMProviderCRUD(t *testing.T) {
	ctx := t.Context()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	p := backends.StoredProvider{
		Name:         "custom-openai",
		DisplayName:  "Custom OpenAI",
		AdapterType:  "openAICompatible",
		BaseURL:      "https://example.com/v1",
		DefaultModel: "example-model",
		SeedModels:   []string{"example-model"},
		Enabled:      true,
	}
	if err := store.UpsertProvider(ctx, p); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	got, err := store.GetProvider(ctx, p.Name)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.Name != p.Name || got.BaseUrl != p.BaseURL || got.DefaultModel != p.DefaultModel {
		t.Fatalf("GetProvider = %+v, want %+v", got, p)
	}
	if rows, err := store.ListProviders(ctx); err != nil || len(rows) != 1 {
		t.Fatalf("ListProviders = %d, %v; want 1, nil", len(rows), err)
	}
	if err := store.DeleteProvider(ctx, p.Name); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if _, err := store.GetProvider(ctx, p.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProvider after delete = %v, want ErrNotFound", err)
	}
}
