package tui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestModelPickerSummaryUsesCachedModelsDevMetadata(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"openai":{"models":{"gpt-new":{"id":"gpt-new","tool_call":true,"reasoning":true,"modalities":{"input":["text","image"]},"limit":{"context":262144},"cost":{"input":1.5,"output":6}}}}}`)
	}))
	t.Cleanup(server.Close)

	source := modelsdev.New(
		cache.NewMemoryCache[string, modelsdev.Snapshot](),
		modelsdev.WithBaseURL(server.URL),
	)
	settings := &engine.Settings{Registry: backends.NewRegistry(backends.WithModelsDevSource(source))}
	if err := source.Warm(t.Context()); err != nil {
		t.Fatalf("warm models.dev: %v", err)
	}
	requests.Store(0)

	ui := tui.New()
	ui.SetSettings(settings)
	ui.SetProviderContext(engine.ProviderSpec{Name: "openai", Model: "gpt-new"}, engine.ProviderSpec{Name: "openai", Model: "gpt-new"})
	ui.SetModelChoices("openai", []string{"gpt-new"})
	ui.SetWorkspace(t.TempDir(), "gpt-new")
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	out := ansi.Strip(model.View().Content)
	for _, want := range []string{"262.1k", "tools,think,vision", "$1.50/6.00/M"} {
		if !strings.Contains(out, want) {
			t.Errorf("model picker %q does not contain %q", out, want)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("models.dev requests = %d, want 0", got)
	}
}
