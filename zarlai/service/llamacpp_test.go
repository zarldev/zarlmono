package service_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
)

func TestLlamaCppClient_UsesProvidedHTTPClient(t *testing.T) {
	var serverHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&serverHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	var transportHits int64
	custom := &http.Client{Transport: &countingTransport{base: http.DefaultTransport, count: &transportHits}}

	client := service.NewLlamaCppClient(srv.URL+"/v1", "test-model", stubEmbedder{},
		service.WithLlamaCppHTTPClient(custom),
	)

	if _, err := client.Chat(t.Context(), []service.Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got := atomic.LoadInt64(&transportHits); got != 1 {
		t.Errorf("injected transport hits = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&serverHits); got != 1 {
		t.Errorf("server hits = %d, want 1", got)
	}
}

func TestLlamaCppClient_ChatStream(t *testing.T) {
	// Simulate llama-server SSE: reasoning deltas, then content deltas,
	// then a native tool_call, then [DONE].
	chunks := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"Think"}}]}`,
		`{"choices":[{"index":0,"delta":{"reasoning_content":"ing..."}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_time","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"tz\":\"UTC\"}"}}]},"finish_reason":"tool_calls"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := service.NewLlamaCppClient(srv.URL+"/v1", "test-model", stubEmbedder{})
	ch := client.ChatStream(t.Context(), []service.Message{{Role: "user", Content: "hi"}}, nil)

	var content, reasoning strings.Builder
	var terminal service.Delta
	for d := range ch {
		if d.Done {
			terminal = d
			continue
		}
		content.WriteString(d.Content)
		reasoning.WriteString(d.Reasoning)
	}

	if terminal.Err != nil {
		t.Fatalf("stream err: %v", terminal.Err)
	}
	if got := content.String(); got != "Hello" {
		t.Errorf("content = %q, want %q", got, "Hello")
	}
	if got := reasoning.String(); got != "Thinking..." {
		t.Errorf("reasoning = %q, want %q", got, "Thinking...")
	}
	if len(terminal.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(terminal.ToolCalls))
	}
	if got := terminal.ToolCalls[0].Function.Name; got != "get_time" {
		t.Errorf("tool name = %q, want get_time", got)
	}
	if got := terminal.ToolCalls[0].Function.Arguments["tz"]; got != "UTC" {
		t.Errorf("tool arg tz = %v, want UTC", got)
	}
}

type countingTransport struct {
	base  http.RoundTripper
	count *int64
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	atomic.AddInt64(c.count, 1)
	return c.base.RoundTrip(r)
}

type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}
