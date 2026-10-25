package engine_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestDefaultSpawnTargetConstructionTerminates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	setMCPSetting(t, settings, prefs.KeyCompactProvider, "(active)")
	// Constructing this built-in target does not fetch credentials or perform
	// inference. The root's fake provider never requests a child execution.
	setMCPSetting(t, settings, prefs.KeySpawnDefaultImplementProvider, "openai-codex")
	setMCPSetting(t, settings, prefs.KeySpawnDefaultImplementModel, "fixture-child")
	setMCPSetting(t, settings, prefs.KeySpawnDefaultExploreProvider, "openai-codex")
	setMCPSetting(t, settings, prefs.KeySpawnDefaultExploreModel, "fixture-explore")
	setMCPSetting(t, settings, prefs.KeySpawnDefaultVerifyProvider, "openai-codex")
	setMCPSetting(t, settings, prefs.KeySpawnDefaultVerifyModel, "fixture-verify")
	requests := 0
	provider := modeProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		requests++
		return modeCalls()
	})
	live := modeLive(t, t.TempDir(), provider, engine.WithSettings(settings))
	live.SetLimits(0, 4, 4, 4)
	assertNoInspectionErrors(t, live.Inspect(t.Context()))
	if requests != 0 {
		t.Fatal("inspection performed inference")
	}
	if err := live.RunTurn(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("hello provider requests = %d", requests)
	}
}
