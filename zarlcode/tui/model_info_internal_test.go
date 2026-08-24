package tui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestModelInfoSummaryDoesNotFetchModelsDev(t *testing.T) {
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

	summary := newModelInfoResolver(settings).summary("openai", "gpt-new")
	for _, want := range []string{"262.1k", "tools,think,vision", "$1.50/6.00/M"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q does not contain %q", summary, want)
		}
	}

	if got := requests.Load(); got != 0 {
		t.Fatalf("models.dev requests = %d, want 0", got)
	}
}
