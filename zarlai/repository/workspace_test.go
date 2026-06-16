package repository_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/repository"
)

func TestWorkspaceRepo_UpsertListGetDelete(t *testing.T) {
	q := testDB(t)
	repo := repository.NewWorkspaceRepo(q)
	ctx := t.Context()

	t.Cleanup(func() { _ = repo.Delete(ctx, "test-acme") })

	// Seed row from migration 019 should be present.
	got, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("initial List: %v", err)
	}
	foundDefault := false
	for _, w := range got {
		if w.Name == "default" {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		t.Fatalf("expected seed 'default' workspace in list, got %+v", got)
	}

	// Upsert a new row.
	w := repository.Workspace{
		Name:          "test-acme",
		Root:          "/tmp/test-acme",
		DefaultBranch: "main",
		Description:   "Acme monorepo",
	}
	if err := repo.Upsert(ctx, w); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Get it back.
	got2, err := repo.Get(ctx, "test-acme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got2.Root != "/tmp/test-acme" || got2.DefaultBranch != "main" {
		t.Errorf("unexpected row: %+v", got2)
	}

	// Missing → sql.ErrNoRows wrapped.
	if _, err := repo.Get(ctx, "does-not-exist"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("missing Get err = %v, want wrap of sql.ErrNoRows", err)
	}

	// Delete non-default.
	if err := repo.Delete(ctx, "test-acme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Delete default refused.
	if err := repo.Delete(ctx, "default"); err == nil {
		t.Errorf("Delete default must be refused")
	}
}
