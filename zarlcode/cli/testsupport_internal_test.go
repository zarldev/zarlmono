package cli

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
