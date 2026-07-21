package engine

import (
	"testing"
	"time"

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
