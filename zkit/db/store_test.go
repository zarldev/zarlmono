package db_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

// openTempStore returns a Store backed by a fresh sqlite file in
// t.TempDir(). Migrations run as part of Open, so the returned
// Store is schema-current.
func openTempStore(t *testing.T) *db.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStore_OpenRunsMigrations(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	// If migrations did not run, the next call would error.
	if _, err := s.ListSessions(t.Context(), "ws"); err != nil {
		t.Fatalf("ListSessions on fresh db: %v", err)
	}
}

func TestStore_OpenHardensPermissions(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("stat dir: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 700", got)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat db: %v", err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("db mode = %o, want 600", got)
	}
}

func TestStore_SessionRoundtrip(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	in := db.SessionRecord{
		ID:             "sess-1",
		Workspace:      "/tmp/ws",
		Label:          "first",
		AgentName:      "default",
		Provider:       "anthropic",
		Model:          "claude-opus-4-7",
		HistoryJSON:    []byte(`[{"role":"user","content":"hi"}]`),
		PendingJSON:    []byte(`[]`),
		LastUsageJSON:  []byte(`{"input":10,"output":5}`),
		DiffBodiesJSON: []byte(`{"foo.go":"--- a\n+++ b\n"}`),
		PlanJSON:       []byte(`{"title":"do the thing","steps":[{"text":"step one","status":"done"}]}`),
	}
	if err := s.SaveSession(ctx, in); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := s.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.Label != "first" || string(out.HistoryJSON) != string(in.HistoryJSON) {
		t.Errorf("roundtrip mismatch: %+v", out)
	}
	if string(out.DiffBodiesJSON) != string(in.DiffBodiesJSON) {
		t.Errorf("diff bodies dropped: %q", out.DiffBodiesJSON)
	}
	if string(out.PlanJSON) != string(in.PlanJSON) {
		t.Errorf("plan dropped: %q", out.PlanJSON)
	}
	if out.CreatedAt.IsZero() || out.UpdatedAt.IsZero() {
		t.Errorf("timestamps not populated: created=%v updated=%v", out.CreatedAt, out.UpdatedAt)
	}
}

func TestStore_GetSessionNotFound(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	_, err := s.GetSession(t.Context(), "no-such-id")
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_ListSessionsByWorkspace(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	mustSave(t, s, db.SessionRecord{ID: "a", Workspace: "ws1", Label: "first"})
	mustSave(t, s, db.SessionRecord{ID: "b", Workspace: "ws2", Label: "second"})
	mustSave(t, s, db.SessionRecord{ID: "c", Workspace: "ws1", Label: "third"})

	got, err := s.ListSessions(ctx, "ws1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (only ws1 entries): %+v", len(got), got)
	}
	// Workspace filter excludes "b"; order between same-second siblings
	// is undefined (updated_at is second-resolution).
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids["a"] || !ids["c"] {
		t.Errorf("missing ws1 sessions, got %+v", got)
	}
}

func TestStore_SettingsWorkspaceFallback(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	if err := s.SetSetting(ctx, "", "theme", "dark"); err != nil {
		t.Fatalf("set global: %v", err)
	}
	// Workspace inherits global.
	v, ok, err := s.GetSetting(ctx, "ws1", "theme")
	if err != nil || !ok || v != "dark" {
		t.Errorf("global fallback: v=%q ok=%v err=%v", v, ok, err)
	}
	// Override at workspace scope.
	if err := s.SetSetting(ctx, "ws1", "theme", "light"); err != nil {
		t.Fatalf("set local: %v", err)
	}
	v, ok, err = s.GetSetting(ctx, "ws1", "theme")
	if err != nil || !ok || v != "light" {
		t.Errorf("override: v=%q ok=%v err=%v", v, ok, err)
	}
	// Other workspace still sees global.
	v, ok, err = s.GetSetting(ctx, "ws2", "theme")
	if err != nil || !ok || v != "dark" {
		t.Errorf("other ws still global: v=%q ok=%v err=%v", v, ok, err)
	}
	// Absent key — neither error nor found.
	_, ok, err = s.GetSetting(ctx, "ws1", "missing")
	if err != nil || ok {
		t.Errorf("missing: ok=%v err=%v", ok, err)
	}
}

func TestStore_EffectiveSettingsMerges(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	_ = s.SetSetting(ctx, "", "theme", "dark")
	_ = s.SetSetting(ctx, "", "model", "global-model")
	_ = s.SetSetting(ctx, "ws", "model", "ws-model")

	got, err := s.EffectiveSettings(ctx, "ws")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if got["theme"] != "dark" || got["model"] != "ws-model" {
		t.Errorf("effective merge wrong: %+v", got)
	}
}

func TestStore_APIKeyRoundtripAndFallback(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	gct := db.APIKeyCiphertext{Ciphertext: []byte("CT-global"), Nonce: []byte("nonce-g"), KeyVersion: 1}
	if err := s.SetAPIKey(ctx, "", "anthropic", gct); err != nil {
		t.Fatalf("set global: %v", err)
	}
	out, ok, err := s.GetAPIKey(ctx, "ws", "anthropic")
	if err != nil || !ok {
		t.Fatalf("get with fallback: ok=%v err=%v", ok, err)
	}
	if string(out.Ciphertext) != "CT-global" {
		t.Errorf("fallback returned wrong ciphertext: %q", out.Ciphertext)
	}

	wct := db.APIKeyCiphertext{Ciphertext: []byte("CT-ws"), Nonce: []byte("nonce-w"), KeyVersion: 1}
	if err := s.SetAPIKey(ctx, "ws", "anthropic", wct); err != nil {
		t.Fatalf("set ws: %v", err)
	}
	out, ok, err = s.GetAPIKey(ctx, "ws", "anthropic")
	if err != nil || !ok {
		t.Fatalf("get ws: ok=%v err=%v", ok, err)
	}
	if string(out.Ciphertext) != "CT-ws" {
		t.Errorf("workspace override missed: %q", out.Ciphertext)
	}

	// List unions both scopes.
	_ = s.SetAPIKey(ctx, "", "openai", db.APIKeyCiphertext{Ciphertext: []byte("o"), Nonce: []byte("n"), KeyVersion: 1})
	providers, err := s.ListAPIKeyProviders(ctx, "ws")
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if strings.Join(providers, ",") != "anthropic,openai" {
		t.Errorf("union wrong: %v", providers)
	}
}

func TestStore_DeleteEmptySessionIgnoresContent(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	mustSave(t, s, db.SessionRecord{
		ID:          "with-content",
		Workspace:   "ws",
		HistoryJSON: []byte(`[{"role":"user","content":"x"}]`),
	})
	mustSave(t, s, db.SessionRecord{ID: "empty", Workspace: "ws"})

	if err := s.DeleteEmptySession(ctx, "with-content"); err != nil {
		t.Fatalf("delete (should be no-op): %v", err)
	}
	if _, err := s.GetSession(ctx, "with-content"); err != nil {
		t.Errorf("non-empty session was deleted: %v", err)
	}

	if err := s.DeleteEmptySession(ctx, "empty"); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
	_, err := s.GetSession(ctx, "empty")
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("empty session not deleted: err=%v", err)
	}
}

func mustSave(t *testing.T, s *db.Store, r db.SessionRecord) {
	t.Helper()
	if err := s.SaveSession(t.Context(), r); err != nil {
		t.Fatalf("save %q: %v", r.ID, err)
	}
}

func TestStore_HeadlessRun_Roundtrip(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	start := db.HeadlessRunStart{
		ID:         "task-123",
		Workspace:  "/ws/foo",
		BaseCommit: "abc123",
		Prompt:     "fix the bug in foo.go",
		StartedAt:  time.Unix(1700000000, 0),
	}
	if err := s.InsertHeadlessRun(ctx, start); err != nil {
		t.Fatalf("InsertHeadlessRun: %v", err)
	}

	// Read-back before completion: row exists but EndedAt is nil.
	mid, err := s.GetHeadlessRun(ctx, start.ID)
	if err != nil {
		t.Fatalf("GetHeadlessRun (mid): %v", err)
	}
	if mid.EndedAt != nil {
		t.Errorf("mid.EndedAt = %v, want nil", mid.EndedAt)
	}
	if mid.Prompt != start.Prompt {
		t.Errorf("mid.Prompt = %q, want %q", mid.Prompt, start.Prompt)
	}

	// Complete with summary.
	in := int64(1234)
	out := int64(5678)
	summary := db.HeadlessRunSummary{
		EndedAt:        time.Unix(1700000060, 0),
		TerminalReason: "completed",
		FinalContent:   "done",
		FinalDiff:      "diff --git a/foo.go ...",
		Iterations:     7,
		ToolCalls:      12,
		TokensIn:       &in,
		TokensOut:      &out,
		Duration:       60 * time.Second,
		Escalated:      false,
	}
	if err := s.CompleteHeadlessRun(ctx, start.ID, summary); err != nil {
		t.Fatalf("CompleteHeadlessRun: %v", err)
	}

	got, err := s.GetHeadlessRun(ctx, start.ID)
	if err != nil {
		t.Fatalf("GetHeadlessRun (final): %v", err)
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(summary.EndedAt) {
		t.Errorf("EndedAt = %v, want %v", got.EndedAt, summary.EndedAt)
	}
	if got.TerminalReason != "completed" {
		t.Errorf("TerminalReason = %q, want completed", got.TerminalReason)
	}
	if got.Iterations != 7 || got.ToolCalls != 12 {
		t.Errorf("counts = %d/%d, want 7/12", got.Iterations, got.ToolCalls)
	}
	if got.TokensIn == nil || *got.TokensIn != 1234 {
		t.Errorf("TokensIn = %v, want 1234", got.TokensIn)
	}
	if got.Duration != 60*time.Second {
		t.Errorf("Duration = %v, want 60s", got.Duration)
	}
}

func TestStore_HeadlessRun_NotFound(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)

	_, err := s.GetHeadlessRun(t.Context(), "missing")
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("GetHeadlessRun missing: err = %v, want ErrNotFound", err)
	}
}

func TestStore_HeadlessRun_ListByWorkspace(t *testing.T) {
	t.Parallel()
	s := openTempStore(t)
	ctx := t.Context()

	for i, ws := range []string{"/ws/a", "/ws/a", "/ws/b"} {
		err := s.InsertHeadlessRun(ctx, db.HeadlessRunStart{
			ID:        "task-" + strings.Repeat("x", i+1),
			Workspace: ws,
			Prompt:    "p",
			StartedAt: time.Unix(int64(1700000000+i), 0),
		})
		if err != nil {
			t.Fatalf("InsertHeadlessRun #%d: %v", i, err)
		}
	}

	got, err := s.ListHeadlessRunsByWorkspace(ctx, "/ws/a", 10)
	if err != nil {
		t.Fatalf("ListHeadlessRunsByWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2", len(got))
	}
	// Newest first.
	if len(got) >= 2 && got[0].StartedAt.Before(got[1].StartedAt) {
		t.Errorf("rows not in newest-first order")
	}
}

// TestOpen_RefusesNewerSchema verifies the newer-binary guard: a DB carrying a
// migration version this binary doesn't know is rejected rather than written.
func TestOpen_RefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Simulate a newer zarlcode having applied a future migration.
	if _, err := s.DB().ExecContext(t.Context(),
		"INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (99999, 1, datetime('now'))"); err != nil {
		t.Fatalf("seed future version: %v", err)
	}
	_ = s.Close()

	// Reopening with this (older) binary must refuse rather than write blind.
	if _, err := db.Open(t.Context(), path); err == nil {
		t.Fatal("Open should refuse a DB whose schema is newer than the binary")
	} else if !strings.Contains(err.Error(), "newer than this binary") {
		t.Errorf("error = %v, want 'newer than this binary'", err)
	}
}
