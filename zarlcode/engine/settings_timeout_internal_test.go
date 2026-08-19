package engine

import (
	"context"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// ResponseTimeout defaults to 90s, honours a positive override, and floors a
// non-positive value back to the default so a stray 0 can't disable the
// stall watchdog and wedge a run forever.
func TestResponseTimeout(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()

	if got := s.ResponseTimeout(ctx); got != 90*time.Second {
		t.Fatalf("default = %s, want 90s", got)
	}

	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyResponseTimeout, "300"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := s.ResponseTimeout(ctx); got != 300*time.Second {
		t.Fatalf("override = %s, want 300s", got)
	}

	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyResponseTimeout, "0"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := s.ResponseTimeout(ctx); got != 90*time.Second {
		t.Fatalf("zero should floor to default, got %s", got)
	}
}

func TestNewSettings_PureConstructionBorrowsStore(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	src := modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot]())
	settings := NewSettings(store, nil, src, t.TempDir())
	if settings.modelsDev != src {
		t.Fatal("NewSettings replaced the supplied models.dev source")
	}
	if settings.warmDone != nil {
		t.Fatal("NewSettings started a warm worker")
	}
	if err := settings.Close(); err != nil {
		t.Fatalf("close borrowed settings: %v", err)
	}
	if err := store.SetSetting(t.Context(), t.TempDir(), "still-open", "yes"); err != nil {
		t.Fatalf("NewSettings closed borrowed store: %v", err)
	}
}

func TestSettingsClose_CancelsAndWaitsForWarmWorker(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	s := &Settings{modelsDev: modelsdev.New(cache.NewMemoryCache[string, modelsdev.Snapshot]())}
	ctx, cancel := context.WithCancel(t.Context())
	s.warmCancel = cancel
	s.warmDone = make(chan struct{})
	go func() {
		close(started)
		<-ctx.Done()
		close(released)
		close(s.warmDone)
	}()
	<-started
	if err := s.Close(); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for warm worker")
	}
}

// SpawnFanoutCap defaults to the standard cap, honours an override, and passes
// 0 through as uncapped.
func TestSpawnFanoutCap(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()

	if got := s.SpawnFanoutCap(ctx); got != coderunner.StandardSpawnFanoutCap {
		t.Fatalf("default = %d, want %d", got, coderunner.StandardSpawnFanoutCap)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeySpawnFanoutCap, "4"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := s.SpawnFanoutCap(ctx); got != 4 {
		t.Fatalf("override = %d, want 4", got)
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeySpawnFanoutCap, "0"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := s.SpawnFanoutCap(ctx); got != 0 {
		t.Fatalf("zero = %d, want 0 (uncapped)", got)
	}
}

// AutoCompact defaults on and flips off only for the explicit "manual" value.
func TestAutoCompact(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()

	if !s.AutoCompact(ctx) {
		t.Fatal("default should be auto (true)")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyCompactionMode, "manual"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if s.AutoCompact(ctx) {
		t.Fatal("manual should disable auto-compaction")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyCompactionMode, "auto"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !s.AutoCompact(ctx) {
		t.Fatal("auto should re-enable auto-compaction")
	}
}
