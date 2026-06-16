package repository_test

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// The Dolt probe runs once for the whole package: when the database is
// down every test must skip, and paying NewDB's ping timeout per test
// turns a skip-everything run into minutes. The pool is shared across
// tests and closed by process exit.
var (
	dbOnce sync.Once
	dbConn *sql.DB
	errDB  error
)

func testDB(t *testing.T) *gen.Queries {
	t.Helper()
	dbOnce.Do(func() {
		dsn := os.Getenv("DOLT_DSN")
		if dsn == "" {
			dsn = "root:@tcp(localhost:3307)/zarl?parseTime=true"
		}
		dbConn, errDB = repository.NewDB(dsn)
	})
	if errDB != nil {
		t.Skipf("dolt not available: %v", errDB)
	}
	return gen.New(dbConn)
}

func TestPromptRepoCreateAndGetActive(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPromptRepo(q)

	var prevActiveID repository.PromptID
	if prev, err := repo.GetActive(ctx); err == nil {
		prevActiveID = prev.ID
	}

	p, err := repo.Create(ctx, "test-prompt", "You are a test bot.")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() {
		if prevActiveID != "" {
			repo.SetActive(ctx, prevActiveID)
		}
		repo.Delete(ctx, p.ID)
	})

	if err := repo.SetActive(ctx, p.ID); err != nil {
		t.Fatalf("set active: %v", err)
	}

	active, err := repo.GetActive(ctx)
	if err != nil {
		t.Fatalf("get active after switch: %v", err)
	}
	if active.ID != p.ID {
		t.Errorf("active id = %q, want %q", active.ID, p.ID)
	}
	if !active.Active {
		t.Error("expected active = true")
	}
}

func TestPromptRepoUpdateContent(t *testing.T) {
	q := testDB(t)
	ctx := context.Background()
	repo := repository.NewPromptRepo(q)

	p, err := repo.Create(ctx, "update-test", "original content")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { repo.Delete(ctx, p.ID) })

	if err := repo.UpdateContent(ctx, p.ID, "updated content"); err != nil {
		t.Fatalf("update: %v", err)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, pr := range all {
		if pr.ID == p.ID && pr.Content != "updated content" {
			t.Errorf("content = %q, want updated content", pr.Content)
		}
	}
}
