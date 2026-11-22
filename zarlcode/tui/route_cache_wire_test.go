package tui_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRouteCacheAndProtocolEditsApplyTogether(t *testing.T) {
	_, ui, live, _ := newCacheEditorUI(t)
	openRouteEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	saveRouteChoice(t, ui, 0, 3)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEscape})
	step(t, ui, textKey("c"))
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	requireCacheView(t, ui, "Resolved protocol for choices: messages")
	cacheChoice(t, ui, 5)
	closeCacheEditor(t, ui)
	target := live.RunTarget()
	if target.Facts == nil || target.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLMESSAGES ||
		target.Facts.CachePolicy.StaticTTL != backends.RouteCacheTTLs.CACHETTL1H || target.Facts.CachePolicy.RollingTTL != backends.RouteCacheTTLs.CACHETTL1H {
		t.Fatalf("combined refresh lost a policy: %+v", target.Facts)
	}
}

func TestRouteCacheRefreshUsesSelectedMarkersOnWire(t *testing.T) {
	type wirePart struct {
		CacheControl struct {
			Type string `json:"type"`
			TTL  string `json:"ttl"`
		} `json:"cache_control"`
	}
	type request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		select {
		case requests <- body:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	s, ui, live, spec := newCacheEditorUI(t)
	spec.BaseURL = server.URL
	prepared, err := s.PrepareTargetCached(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	live.ApplyTarget(prepared)
	ui.SetProviderContext(spec, spec)
	openCacheEditor(t, ui, false)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyEnter})
	cacheChoice(t, ui, 6) // Chat system marker, one hour.
	closeCacheEditor(t, ui)
	for _, err := range live.RunTarget().Provider.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "stable prefix"}, {Role: llm.RoleUser, Content: "hello"}},
	}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	var body request
	select {
	case body = <-requests:
	default:
		t.Fatal("completion returned without issuing a request")
	}
	if body.Model != spec.Model || len(body.Messages) != 2 || body.Messages[0].Role != llm.RoleSystem {
		t.Fatal("cache refresh changed the requested alias or message order")
	}
	var parts []wirePart
	if err := json.Unmarshal(body.Messages[0].Content, &parts); err != nil || len(parts) != 1 || parts[0].CacheControl.Type != "ephemeral" || parts[0].CacheControl.TTL != "1h" {
		t.Fatalf("selected cache marker absent from actual request: %+v: %v", parts, err)
	}
}
