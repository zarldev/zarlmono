package litellm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestConformance(t *testing.T) {
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			success := func(w http.ResponseWriter, _ *http.Request) { fixture.serve(w, true, true) }
			tool := func(w http.ResponseWriter, _ *http.Request) { fixture.serve(w, true, false) }
			failure := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "private error", http.StatusUnauthorized) }
			providertest.Run(t, providertest.Suite{
				Factory: func(_ *testing.T, baseURL string) llm.Provider {
					return litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(baseURL), litellm.WithProtocol(fixture.protocol), litellm.WithModel(alias))
				},
				Scenarios: []providertest.Scenario{
					{Name: "success EOF", Handler: success, Request: providertest.SimpleRequest("hi"), Assert: providertest.AssertUsageReported},
					{Name: "tool call", Handler: tool, Request: providertest.RequestWithTool("echo hi"), Assert: providertest.AssertToolCallEmitted("echo")},
					{Name: "pre canceled", Handler: success, PreCancel: true, Assert: providertest.AssertCancellationHonoured},
					{Name: "downstream stop", Handler: success, Request: providertest.SimpleRequest("hi"), StopAfter: 1, Assert: func(t *testing.T, chunks []llm.CompletionChunk, err error) {
						t.Helper()
						if err != nil || len(chunks) != 1 {
							t.Fatalf("stop chunks = %d, error = %v", len(chunks), err)
						}
					}},
					{Name: "terminal HTTP error", Handler: failure, Request: providertest.SimpleRequest("hi"), Assert: func(t *testing.T, _ []llm.CompletionChunk, err error) {
						t.Helper()
						if !errors.Is(err, llm.ErrInvalidAPIKey) {
							t.Fatalf("terminal error = %v", err)
						}
					}},
				},
			})
		})
	}
}

func TestLazyUnsupportedProtocolAndConstruction(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unexpected HTTP") })}
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLUNKNOWN, llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLGENERATECONTENT} {
		p := litellm.NewProvider("", litellm.WithBaseURL(":invalid"), litellm.WithProtocol(protocol), litellm.WithHTTPClient(client))
		stream := p.Complete(t.Context(), llm.CompletionRequest{})
		if stream == nil || calls.Load() != 0 {
			t.Fatal("eager Complete work")
		}
	}
	p := litellm.NewProvider("ordinary-test-key", litellm.WithProtocol(llm.ModelProtocols.PROTOCOLUNKNOWN), litellm.WithHTTPClient(client))
	var yields int
	for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{}) {
		yields++
		if !errors.Is(err, llm.ErrModelNotSupported) || chunk.Content != "" {
			t.Fatalf("unknown protocol = %+v, %v", chunk, err)
		}
	}
	if yields != 1 || calls.Load() != 0 {
		t.Fatalf("unknown protocol yields = %d, requests = %d", yields, calls.Load())
	}
	// A Google construction error is an iteration error, not an eager error or
	// fallback to a different protocol.
	google := litellm.NewProvider("", litellm.WithProtocol(llm.ModelProtocols.PROTOCOLGENERATECONTENT), litellm.WithHTTPClient(client))
	for _, err := range google.Complete(t.Context(), llm.CompletionRequest{}) {
		if !errors.Is(err, llm.ErrInvalidAPIKey) {
			t.Fatalf("Google setup error = %v", err)
		}
	}
}

func TestCancellationDuringHTTPRead(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			cause := errors.New("stop gateway read")
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				cancel(cause)
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL), litellm.WithProtocol(fixture.protocol), litellm.WithModel(alias))
			var terminal error
			for _, err := range p.Complete(ctx, providertest.SimpleRequest("hi")) {
				if err != nil {
					terminal = err
				}
			}
			if !errors.Is(terminal, cause) {
				t.Fatalf("cancellation = %v", terminal)
			}
		})
	}
}

func TestNoModelAliasDefault(t *testing.T) {
	t.Parallel()
	p := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) == "" {
			t.Error("missing request body")
		}
		// Model is explicitly empty instead of inheriting the native codec default.
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != nil && body["model"] != "" {
			t.Errorf("default model = %#v", body["model"])
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}))
	if p.Protocol() != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatal("incorrect default protocol")
	}
	for range p.Complete(t.Context(), providertest.SimpleRequest("hi")) {
	}
}
