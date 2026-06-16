package repository_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/repository"
)

func TestTaskProfileOverrideRepo_upsert_get_delete_roundtrip(t *testing.T) {
	q := testDB(t)
	repo := repository.NewTaskProfileOverrideRepo(q)
	ctx := t.Context()

	// Clean up any leftover state from prior runs.
	t.Cleanup(func() { _ = repo.Delete(ctx, "test-researcher") })

	model := "gemma4:31b"
	prefix := "You are a fast reader."
	maxIter := int32(5)

	if err := repo.Upsert(ctx, "test-researcher", repository.TaskProfileOverride{
		Model:         &model,
		PromptPrefix:  &prefix,
		MaxIterations: &maxIter,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.Get(ctx, "test-researcher")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Model == nil || *got.Model != model {
		t.Errorf("Model = %v, want %q", got.Model, model)
	}
	if got.PromptPrefix == nil || *got.PromptPrefix != prefix {
		t.Errorf("PromptPrefix = %v, want %q", got.PromptPrefix, prefix)
	}
	if got.MaxIterations == nil || *got.MaxIterations != maxIter {
		t.Errorf("MaxIterations = %v, want %d", got.MaxIterations, maxIter)
	}

	if err := repo.Delete(ctx, "test-researcher"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	empty, err := repo.Get(ctx, "test-researcher")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if empty.Model != nil || empty.PromptPrefix != nil || empty.MaxIterations != nil {
		t.Errorf("expected zero override after delete, got %+v", empty)
	}
}

func TestTaskProfileOverrideRepo_get_missing_returns_zero_no_error(t *testing.T) {
	q := testDB(t)
	repo := repository.NewTaskProfileOverrideRepo(q)
	got, err := repo.Get(t.Context(), "does-not-exist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Model != nil || got.PromptPrefix != nil || got.MaxIterations != nil {
		t.Errorf("expected zero override, got %+v", got)
	}
}

func TestTaskProfileOverrideRepo_partial_override_only_sets_named_fields(t *testing.T) {
	q := testDB(t)
	repo := repository.NewTaskProfileOverrideRepo(q)
	ctx := t.Context()
	t.Cleanup(func() { _ = repo.Delete(ctx, "test-researcher-partial") })

	model := "gemma4:31b"
	if err := repo.Upsert(ctx, "test-researcher-partial", repository.TaskProfileOverride{Model: &model}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.Get(ctx, "test-researcher-partial")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Model == nil || *got.Model != model {
		t.Errorf("Model = %v, want %q", got.Model, model)
	}
	if got.PromptPrefix != nil {
		t.Errorf("PromptPrefix = %v, want nil", got.PromptPrefix)
	}
	if got.MaxIterations != nil {
		t.Errorf("MaxIterations = %v, want nil", got.MaxIterations)
	}
}
