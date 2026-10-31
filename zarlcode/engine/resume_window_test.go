package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestStoredSessionUnknownWindowDoesNotInheritPreviousRoute(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), t.TempDir()+"/preferences.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.SetContextWindow(1_000_000)
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if err := reservation.RestoreStoredSession(t.Context(), engine.StoredSessionRestore{
		ID: "unknown-window-session", UseSavedTarget: true,
		Target: engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "unknown", Model: "unknown"}},
	}, settings.Svc); err != nil {
		t.Fatal(err)
	}
	if got := live.RunTarget().Window; got != engine.LiveContextWindow {
		t.Fatalf("resumed context window = %d, want %d", got, engine.LiveContextWindow)
	}
}
