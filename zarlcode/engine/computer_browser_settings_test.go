package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestComputerBrowserVisible(t *testing.T) {
	root := t.TempDir()
	store, err := db.Open(t.Context(), root+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, root)

	if settings.ComputerBrowserVisible(t.Context()) {
		t.Fatal("computer browser should be hidden by default")
	}
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyComputerBrowserVisible, "on"); err != nil {
		t.Fatal(err)
	}
	if !settings.ComputerBrowserVisible(t.Context()) {
		t.Fatal("computer browser should be visible when enabled")
	}
}
