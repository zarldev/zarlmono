//go:build linux

package tui_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
	"github.com/zarldev/zarlmono/zkit/db"
)

// Exercise the launch wiring, not just Submit's returned command: persisted
// limits, settings-backed default child routes, MCP, real key input and shutdown.
func TestBeforeCheckpointDispatchWithSpawnDepthPTY(t *testing.T) {
	for _, depth := range []string{"2", "16"} {
		t.Run(depth, func(t *testing.T) {
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ws.Close() })
			store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			settings := engine.NewSettings(store, nil, nil, ws.Root())
			if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeySpawnMaxDepth, depth); err != nil {
				t.Fatal(err)
			}
			f := &beforeFixture{ws: ws, store: store, provider: &beforeProvider{}}
			f.sink = teasink.New(nil)
			t.Cleanup(f.sink.Close)
			f.live = engine.NewLiveRunner(f.provider, ws, "saved-model", engine.WithSettings(settings), engine.WithLiveSink(f.sink))
			f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
			limits := settings.Limits(t.Context())
			f.live.SetLimits(limits.ReserveTokens, limits.MaxIterations, limits.SpawnMaxIterations, limits.SpawnMaxDepth)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
				defer cancel()
				if err := f.live.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			f.ui = tui.New()
			f.ui.SetLiveRunner(f.live)
			f.ui.SetLiveEventSink(f.sink)
			f.ui.SetSettings(settings)
			f.provider.check = func(ctx context.Context) {
				id, err := store.GetSettingExact(ctx, ws.Root(), "active_session")
				if err != nil {
					t.Error(err)
					return
				}
				checkpoints, err := store.ListSessionCheckpoints(ctx, id)
				if err != nil || len(checkpoints) == 0 {
					t.Errorf("provider dispatched without a durable BEFORE checkpoint: %v", err)
				}
			}
			host := tools.NewRegistry()
			registry := dynamic.NewMCPRegistry(host, nil)
			f.live.AttachMCP(registry, host)
			terminal := startRewindTerminal(t, f)
			terminal.waitText(t, "▏")
			for _, prompt := range []string{"protected production prompt", "next production prompt"} {
				terminal.keys(t, prompt+"\r")
				terminal.wait(t, "durable turn settlement with spawn defaults", func() bool {
					id, err := store.GetSettingExact(t.Context(), ws.Root(), "active_session")
					return err == nil && terminalSettled(t, f, id, prompt)
				})
			}
			terminal.stop(t)
			if got, err := store.GetSettingExact(t.Context(), ws.Root(), prefs.KeySpawnMaxDepth); err != nil || got != depth {
				t.Errorf("persisted spawn depth = %q, want %q: %v", got, depth, err)
			}
			reservation, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			reservation.Release()
		})
	}
}
