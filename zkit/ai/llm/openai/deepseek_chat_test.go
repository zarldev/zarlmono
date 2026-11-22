package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestDeepSeekChatInvalidControlIsLazyAndSafe(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, value := range []any{"private-invalid-effort", "", " ", "LOW", 1, true, nil, map[string]any{"private": "value"}} {
			t.Run(fmt.Sprintf("stream=%v/%T/%v", streaming, value, value), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(server.Close)
				provider := deepseek.NewProvider("test", deepseek.WithBaseURL(server.URL), deepseek.WithModel("opaque"))
				req := llm.CompletionRequest{Stream: streaming, Options: llm.ModelOptions{"reasoning_effort": value}}
				stream := provider.Complete(t.Context(), req)
				if calls.Load() != 0 {
					t.Fatal("Complete performed eager work")
				}
				yields := 0
				stream(func(chunk llm.CompletionChunk, err error) bool {
					yields++
					if !errors.Is(err, openai.ErrDeepSeekChatControl) || errors.Is(err, openai.ErrParseReasoningEffort) {
						t.Fatalf("error identity=%v", err)
					}
					if err.Error() != "chat controls: invalid deepseek chat control" {
						t.Fatalf("unsafe error=%v", err)
					}
					if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
						t.Fatalf("terminal chunk=%+v", chunk)
					}
					return false
				})
				if yields != 1 || calls.Load() != 0 {
					t.Fatalf("yields=%d calls=%d", yields, calls.Load())
				}
				ctx, cancel := context.WithCancelCause(t.Context())
				cause := errors.New("caller cancelled")
				cancel(cause)
				for _, err := range provider.Complete(ctx, req) {
					if !errors.Is(err, cause) {
						t.Fatalf("cancellation error=%v", err)
					}
				}
				if calls.Load() != 0 {
					t.Fatal("cancelled request dispatched")
				}
			})
		}
	}
}

func deepSeekIsolationServer(t *testing.T, streaming bool) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire map[string]any
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- wire
		const usage = `"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":40}`
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],`+usage+"}\n\ndata: [DONE]\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+usage+`}`)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestDeepSeekChatExplicitPolicyIsolation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			build  func(string) llm.Provider
			native bool
		}{
			{name: "explicit-native-opaque", native: true, build: func(url string) llm.Provider {
				return openai.NewProvider("test", openai.WithBaseURL(url), openai.WithModel("opaque"), openai.WithDeepSeekChatDialect(true))
			}},
			{name: "custom-native-model-no-inference", build: func(url string) llm.Provider {
				return openai.NewProvider("test", openai.WithBaseURL(url+"/api.deepseek.com"), openai.WithModel("deepseek-v4-flash"))
			}},
			{name: "explicit-false", build: func(url string) llm.Provider {
				return openai.NewProvider("test", openai.WithBaseURL(url), openai.WithModel("deepseek-v4-flash"), openai.WithDeepSeekChatDialect(true), openai.WithDeepSeekChatDialect(false))
			}},
			{name: "ordinary-openai", build: func(url string) llm.Provider {
				// Redirect only transport, retaining the hosted URL policy.
				client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
					forward := req.Clone(req.Context())
					forward.URL.Scheme, forward.URL.Host = "http", strings.TrimPrefix(url, "http://")
					return http.DefaultTransport.RoundTrip(forward)
				})}
				return openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
			}},
			{name: "llamacpp", build: func(url string) llm.Provider {
				return llamacpp.NewProvider(llamacpp.WithBaseURL(url), llamacpp.WithModel("deepseek-v4-flash"))
			}},
			{name: "ollama", build: func(url string) llm.Provider {
				return ollama.NewProvider(ollama.WithBaseURL(url), ollama.WithModel("deepseek-v4-flash"))
			}},
			{name: "litellm", build: func(url string) llm.Provider {
				return litellm.NewProvider("test", litellm.WithBaseURL(url), litellm.WithModel("deepseek-v4-flash"))
			}},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, tc.name), func(t *testing.T) {
				server, requests := deepSeekIsolationServer(t, streaming)
				provider := tc.build(server.URL)
				req := llm.CompletionRequest{Stream: streaming, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Thinking: llm.ThinkingConfig{Enabled: true}, Options: llm.ModelOptions{"reasoning_effort": "low", "thinking": map[string]any{"type": "disabled"}, "private-option": "not-forwarded"}}
				var usage llm.Usage
				for chunk, err := range provider.Complete(t.Context(), req) {
					if err != nil {
						t.Fatal(err)
					}
					if chunk.UsageReported {
						usage = chunk.Usage
					}
				}
				wire := <-requests
				if _, found := wire["private-option"]; found {
					t.Fatal("arbitrary option forwarded")
				}
				if tc.native {
					if wire["reasoning_effort"] != "low" || !reflect.DeepEqual(wire["thinking"], map[string]any{"type": "enabled"}) || usage.CachedTokens != 60 {
						t.Fatalf("wire=%+v usage=%+v", wire, usage)
					}
				} else {
					if _, found := wire["thinking"]; found {
						t.Fatalf("native thinking leaked: %+v", wire)
					}
					if _, found := wire["reasoning_effort"]; found {
						t.Fatalf("native effort leaked: %+v", wire)
					}
					if usage.CachedTokens != 0 {
						t.Fatalf("native hit alias leaked: %+v", usage)
					}
					// Requests without controls produce the identical ordinary body.
					req.Thinking, req.Options = llm.ThinkingConfig{}, nil
					for _, err := range provider.Complete(t.Context(), req) {
						if err != nil {
							t.Fatal(err)
						}
					}
					if plain := <-requests; !reflect.DeepEqual(wire, plain) {
						t.Fatalf("ordinary body changed with native controls: %+v vs %+v", wire, plain)
					}
				}
			})
		}
	}
}

type deepSeekTrackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *deepSeekTrackedBody) Close() error { b.closed.Store(true); return nil }

func TestDeepSeekChatEarlyStopClosesBody(t *testing.T) {
	body := &deepSeekTrackedBody{Reader: strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"think\"}}]}\n\ndata: [DONE]\n\n")}
	var calls atomic.Int64
	client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: req}, nil
	})}
	provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false), openai.WithDeepSeekChatDialect(true))
	req := llm.CompletionRequest{Stream: true, Options: llm.ModelOptions{"reasoning_effort": "bad"}}
	stream := provider.Complete(t.Context(), req)
	// Parsing is deferred until iteration, even when Complete saw an invalid value.
	req.Options["reasoning_effort"] = "high"
	if calls.Load() != 0 {
		t.Fatal("eager request")
	}
	yields := 0
	stream(func(chunk llm.CompletionChunk, err error) bool {
		yields++
		if err != nil || chunk.Thinking != "think" {
			t.Fatalf("chunk=%+v err=%v", chunk, err)
		}
		return false
	})
	if yields != 1 || calls.Load() != 1 || !body.closed.Load() {
		t.Fatalf("yields=%d calls=%d closed=%v", yields, calls.Load(), body.closed.Load())
	}
}

func TestDeepSeekChatDialectDoesNotAffectResponses(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", streaming), func(t *testing.T) {
			var wire map[string]any
			client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(req.Body).Decode(&wire); err != nil {
					t.Fatal(err)
				}
				if req.URL.Path != "/v1/responses" {
					t.Fatalf("path=%s", req.URL.Path)
				}
				response := `{"id":"r","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110,"prompt_cache_hit_tokens":60}}`
				kind := "application/json"
				if streaming {
					kind, response = "text/event-stream", "data: {\"type\":\"response.completed\",\"response\":"+response+"}\n\n"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
			})}
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithDeepSeekChatDialect(true), openai.WithResponsesAPI(true))
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: streaming, Options: llm.ModelOptions{"reasoning_effort": "bad"}}) {
				if err != nil {
					t.Fatal(err)
				}
				if chunk.Usage.CachedTokens != 0 {
					t.Fatalf("Chat cache fallback leaked: %+v", chunk.Usage)
				}
			}
			if _, found := wire["thinking"]; found {
				t.Fatalf("Chat thinking leaked: %+v", wire)
			}
			if _, found := wire["reasoning_effort"]; found {
				t.Fatalf("Chat effort leaked: %+v", wire)
			}
		})
	}
}
