package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestNewSettingsExposesCompositionDependencies(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workspace := t.TempDir()
	settings := engine.NewSettings(store, nil, nil, workspace)
	if settings.Store != store {
		t.Fatal("NewSettings did not retain the supplied store")
	}
	if settings.Svc == nil {
		t.Fatal("NewSettings did not construct a preference service")
	}
	if settings.Registry == nil {
		t.Fatal("NewSettings did not construct a provider registry")
	}
	if got := settings.WorkspaceRoot(); got != workspace {
		t.Fatalf("WorkspaceRoot() = %q, want %q", got, workspace)
	}
}
