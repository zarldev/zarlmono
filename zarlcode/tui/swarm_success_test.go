package tui_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestStartSwarmSettlesRecordedActivationSuccess(t *testing.T) {
	f := newBeforeFixture(t)

	var calls atomic.Int32
	const reasoning = `{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-private-canary","summary":[{"type":"summary_text","text":"Considering"}]}`
	answer := `{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"done"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("expected Responses route, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"delta\":\"Considering\"}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"delta\":\"done\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"output\":[" + reasoning + "," + answer + "],\"usage\":{}}}\n\n"))
	}))
	t.Cleanup(server.Close)

	localURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: settlementTransport(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.URL.Scheme, request.URL.Host = localURL.Scheme, localURL.Host
		return server.Client().Transport.RoundTrip(request)
	})}
	provider := openai.NewProvider("test-key", openai.WithHTTPClient(client), openai.WithBaseURL("https://api.openai.com/v1"), openai.WithModel("gpt-5.6"))
	f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "gpt-5.6"})

	settleRewindTurn(t, f, "seed exact session")

	_ = f.ui.StartSwarm("shared research goal")
	if !strings.Contains(f.ui.ToastText(), "swarm started") {
		t.Fatalf("swarm did not start: %s", f.ui.ToastText())
	}

	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("recorded activation did not dispatch: calls=%d toast=%s", calls.Load(), f.ui.ToastText())
		}
		f.sink.Drain()
		f.mu.Lock()
		events := f.events
		f.events = nil
		f.mu.Unlock()
		if len(events) == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		for i, event := range events {
			_, cmd := f.ui.Update(event)
			if i == len(events)-1 {
				driveBeforeCommand(f, cmd)
			}
		}
	}
	if strings.Contains(f.ui.ToastText(), "swarm activation failed") {
		t.Fatalf("activation failed: %s", f.ui.ToastText())
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls = %d, want 2 (seed + activation)", calls.Load())
	}

	_ = f.ui.StopSwarm()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.sink.Drain()
		f.mu.Lock()
		events := f.events
		f.events = nil
		f.mu.Unlock()
		if len(events) == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		for _, event := range events {
			f.ui.Update(event)
		}
	}
}
