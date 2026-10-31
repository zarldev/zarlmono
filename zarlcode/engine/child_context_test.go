package engine_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSpawnResolvesChildContextAtItsOwnEndpoint(t *testing.T) {
	t.Parallel()
	settings := newJudgeTestSettings(t)
	var probes, completions atomic.Int32
	child := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			probes.Add(1)
			_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":512}}`)
			return
		}
		completions.Add(1)
		completionFixture(w, "child complete", "")
	}))
	t.Cleanup(child.Close)
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "llamacpp", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE, BaseURL: child.URL + "/v1", DefaultModel: "small", Enabled: true, Builtin: true}}))
	for key, value := range map[string]string{prefs.KeySpawnDefaultImplementProvider: "llamacpp", prefs.KeySpawnDefaultImplementModel: "small", prefs.KeySpawnDefaultExploreProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultVerifyProvider: prefs.ActiveModelTarget, prefs.KeyCompactProvider: prefs.ActiveModelTarget, prefs.KeyDecomposeJudge: "off"} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	var rootCalls atomic.Int32
	parent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if rootCalls.Add(1) == 1 {
			completionFixture(w, "", `{"mode":"implement","prompt":"`+strings.Repeat("child context probe ", 600)+`"}`)
		} else {
			completionFixture(w, "done", "")
		}
	}))
	t.Cleanup(parent.Close)
	ws, err := code.NewWorkspace(settings.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(openai.NewProvider("fixture", openai.WithBaseURL(parent.URL)), ws, "fixture", engine.WithSettings(settings))
	live.SetContextWindow(1_000_000)
	live.SetLimits(4096, 5, 1, 1)
	t.Cleanup(func() { _ = live.Close(context.WithoutCancel(t.Context())) })
	if err := live.RunTurn(t.Context(), "delegate"); err != nil {
		t.Fatal(err)
	}
	if probes.Load() == 0 {
		t.Fatal("child context inherited parent budget instead of resolving own endpoint")
	}
	if completions.Load() == 0 {
		t.Fatal("child adapter was not exercised")
	}
}
