package engine_test

import (
	"testing"
	"time"

	. "github.com/zarldev/zarlmono/zarlcode/engine"

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
	if err := settings.Close(); err != nil {
		t.Fatalf("close borrowed settings: %v", err)
	}
	if err := store.SetSetting(t.Context(), t.TempDir(), "still-open", "yes"); err != nil {
		t.Fatalf("NewSettings closed borrowed store: %v", err)
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

func TestSpawnEnabled(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	if s.SpawnEnabled(ctx) {
		t.Fatal("sub-agents should be disabled by default")
	}
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeySpawnEnabled, "on"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !s.SpawnEnabled(ctx) {
		t.Fatal("sub-agents should be enabled after setting on")
	}
	if got := s.Limits(ctx).SpawnMaxDepth; got != 1 {
		t.Fatalf("default spawn depth = %d, want 1 when enabled", got)
	}
}

func TestSpawnModes(t *testing.T) {
	s := newJudgeTestSettings(t)
	ctx := t.Context()
	sets := map[string]string{
		prefs.KeySpawnDefaultExploreAgent:    "researcher",
		prefs.KeySpawnDefaultExploreProvider: "anthropic",
		prefs.KeySpawnDefaultExploreModel:    "claude-fast",
		prefs.KeySpawnExploreMaxIterations:   "4",
		prefs.KeySpawnDefaultVerifyModel:     "judge-small",
	}
	for key, value := range sets {
		if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	got := s.SpawnModes(ctx)
	if got.Explore != (SpawnModeConfig{
		DefaultAgent:  "researcher",
		DefaultTarget: ProviderSpec{Name: "anthropic", Model: "claude-fast"},
		MaxIterations: 4,
	}) {
		t.Fatalf("explore config = %+v", got.Explore)
	}
	if got.Verify.DefaultTarget != (ProviderSpec{Model: "judge-small"}) {
		t.Fatalf("verify config = %+v", got.Verify)
	}
	if got.Implement != (SpawnModeConfig{}) {
		t.Fatalf("implement config = %+v, want inherited", got.Implement)
	}
}
