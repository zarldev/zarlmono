package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

type nativePolicyTransport func(*http.Request) (*http.Response, error)

func (f nativePolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type nativePolicyBody struct {
	io.Reader
	closed *atomic.Int64
}

func (b nativePolicyBody) Close() error { b.closed.Add(1); return nil }

func nativePolicyClient(t *testing.T, inspect func(*http.Request, map[string]any)) (*http.Client, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var calls, closed atomic.Int64
	client := &http.Client{Transport: nativePolicyTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.Host != "api.openai.com" {
			t.Errorf("native test route host = %q", req.URL.Host)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if inspect != nil {
			inspect(req, body)
		}
		responses := req.URL.Path == "/v1/responses"
		stream, _ := body["stream"].(bool)
		contentType := "application/json"
		response := `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
		if responses {
			response = `{"status":"completed","output":[{"type":"message","role":"assistant","id":"msg_test","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}`
		}
		if stream {
			contentType = "text/event-stream"
			response = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			if responses {
				response = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: nativePolicyBody{Reader: strings.NewReader(response), closed: &closed}, Request: req}, nil
	})}
	return client, &calls, &closed
}

func nativePolicyRequest(policy any) llm.CompletionRequest {
	return llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "private-text-canary"}}, Options: llm.ModelOptions{openai.PromptCacheOption: policy}}
}

func nativePolicySelector(message, content any) map[string]any {
	return map[string]any{"message_index": message, "content_index": content}
}

func nativePolicyWantError(t *testing.T, stream llm.CompletionStream, want error) {
	t.Helper()
	yields := 0
	for chunk, err := range stream {
		yields++
		if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) || !errors.Is(err, want) {
			t.Fatalf("terminal chunk = %#v, err = %v; want zero and %v", chunk, err, want)
		}
		if strings.Contains(err.Error(), "canary") || errors.Is(err, openai.ErrParseNativeCacheMode) || errors.Is(err, openai.ErrParseNativeCacheTTL) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
	if yields != 1 {
		t.Errorf("yields = %d; want 1", yields)
	}
}

func nativePolicyDrain(t *testing.T, stream llm.CompletionStream) {
	t.Helper()
	var text strings.Builder
	for chunk, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		text.WriteString(chunk.Content)
	}
	if text.String() != "ok" {
		t.Errorf("completion text = %q", text.String())
	}
}

func TestNativeCachePolicyWire(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{"implicit", "explicit"} {
				for _, ttl := range []string{"", "30m", "12h"} {
					if !responses && ttl == "12h" {
						continue
					}
					name := mode + "/" + ttl
					if responses {
						name += "/responses"
					} else {
						name += "/chat"
					}
					if stream {
						name += "/SSE"
					} else {
						name += "/JSON"
					}
					t.Run(name, func(t *testing.T) {
						policy := llm.ModelOptions{"mode": mode}
						if ttl != "" {
							policy["ttl"] = ttl
						}
						client, calls, closed := nativePolicyClient(t, func(_ *http.Request, body map[string]any) {
							want := map[string]any{"mode": mode}
							if ttl != "" {
								want["ttl"] = ttl
							}
							if !reflect.DeepEqual(body["prompt_cache_options"], want) {
								t.Errorf("cache options = %#v, want %#v", body["prompt_cache_options"], want)
							}
							if body["model"] != "opaque-alias-canary" || body["prompt_cache_key"] != "request-key-canary" {
								t.Errorf("model/key forwarding changed: %#v", body)
							}
							if _, exists := body["prompt_cache_retention"]; exists {
								t.Error("unexpected legacy retention")
							}
						})
						provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithModel("opaque-alias-canary"), openai.WithResponsesAPI(responses), openai.WithPromptCache("inherited-key", ""))
						req := nativePolicyRequest(policy)
						req.Stream = stream
						req.Options["prompt_cache_key"] = "request-key-canary"
						nativePolicyDrain(t, provider.Complete(t.Context(), req))
						if calls.Load() != 1 || closed.Load() != 1 {
							t.Errorf("calls/closed = %d/%d", calls.Load(), closed.Load())
						}
					})
				}
			}
		}
	}
}

func TestNativeCachePolicyInvalidBoundary(t *testing.T) {
	selectorPolicy := func(selector any) any { return map[string]any{"mode": "explicit", "breakpoints": []any{selector}} }
	cases := []struct {
		name   string
		policy any
	}{
		{"null", nil}, {"string", "secret-canary"}, {"typed struct", struct{ Mode string }{"explicit"}}, {"typed map nil", map[string]any(nil)},
		{"missing mode", map[string]any{}}, {"null mode", map[string]any{"mode": nil}}, {"mode number", map[string]any{"mode": 1}},
		{"mode case", map[string]any{"mode": "Explicit"}}, {"mode spaces", map[string]any{"mode": " explicit"}}, {"mode enum constant", map[string]any{"mode": "CACHEEXPLICIT"}},
		{"mode canary", map[string]any{"mode": "secret-canary"}}, {"case member", map[string]any{"Mode": "explicit"}},
		{"unknown member", map[string]any{"mode": "explicit", "secret-canary": true}}, {"prewarm", map[string]any{"mode": "explicit", "prewarm": true}},
		{"null ttl", map[string]any{"mode": "explicit", "ttl": nil}}, {"number ttl", map[string]any{"mode": "explicit", "ttl": 30}},
		{"bad ttl", map[string]any{"mode": "explicit", "ttl": "secret-canary"}}, {"case ttl", map[string]any{"mode": "explicit", "ttl": "30M"}},
		{"null array", map[string]any{"mode": "explicit", "breakpoints": nil}}, {"nil array", map[string]any{"mode": "explicit", "breakpoints": []any(nil)}},
		{"typed array", map[string]any{"mode": "explicit", "breakpoints": []map[string]any{nativePolicySelector(0, 0)}}},
		{"null selector", selectorPolicy(nil)}, {"missing indices", selectorPolicy(map[string]any{})},
		{"missing message", selectorPolicy(map[string]any{"content_index": 0})}, {"missing content", selectorPolicy(map[string]any{"message_index": 0})},
		{"null index", selectorPolicy(nativePolicySelector(nil, 0))}, {"string index", selectorPolicy(nativePolicySelector("0", 0))},
		{"bool index", selectorPolicy(nativePolicySelector(false, 0))}, {"negative", selectorPolicy(nativePolicySelector(-1, 0))},
		{"fraction", selectorPolicy(nativePolicySelector(0.5, 0))}, {"infinite", selectorPolicy(nativePolicySelector(math.Inf(1), 0))},
		{"NaN", selectorPolicy(nativePolicySelector(math.NaN(), 0))}, {"overflow", selectorPolicy(nativePolicySelector(uint64(math.MaxUint64), 0))},
		{"large float", selectorPolicy(nativePolicySelector(math.MaxFloat64, 0))}, {"message bound", selectorPolicy(nativePolicySelector(1, 0))},
		{"content bound", selectorPolicy(nativePolicySelector(0, 1))}, {"content negative", selectorPolicy(nativePolicySelector(0, -1))},
		{"number fractional", selectorPolicy(nativePolicySelector(json.Number("0.00000000000000000001"), 0))},
		{"number rounding", selectorPolicy(nativePolicySelector(json.Number("0.99999999999999999999"), 0))},
		{"number huge exponent", selectorPolicy(nativePolicySelector(json.Number("1e1000000000"), 0))},
		{"number negative exponent", selectorPolicy(nativePolicySelector(json.Number("1e-1000000000"), 0))},
		{"number invalid", selectorPolicy(nativePolicySelector(json.Number("0/1"), 0))},
		{"selector unknown", selectorPolicy(map[string]any{"message_index": 0, "content_index": 0, "secret-canary": true})},
		{"selector case", selectorPolicy(map[string]any{"Message_Index": 0, "content_index": 0})},
		{"selector null ttl", selectorPolicy(map[string]any{"message_index": 0, "content_index": 0, "ttl": nil})},
		{"selector bad ttl", selectorPolicy(map[string]any{"message_index": 0, "content_index": 0, "ttl": "secret-canary"})},
		{"duplicate", map[string]any{"mode": "explicit", "breakpoints": []any{nativePolicySelector(0, 0), nativePolicySelector(0, 0)}}},
		{"explicit excess", map[string]any{"mode": "explicit", "breakpoints": make([]any, 5)}},
		{"implicit excess", map[string]any{"mode": "implicit", "breakpoints": make([]any, 4)}},
	}
	for _, responses := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name+map[bool]string{false: "/chat", true: "/responses"}[responses], func(t *testing.T) {
				client, calls, _ := nativePolicyClient(t, nil)
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(responses))
				nativePolicyWantError(t, provider.Complete(t.Context(), nativePolicyRequest(tc.policy)), openai.ErrInvalidCachePolicy)
				if calls.Load() != 0 {
					t.Errorf("HTTP calls = %d", calls.Load())
				}
			})
		}
	}
}

func TestNativeCachePolicyIntegralIndices(t *testing.T) {
	indices := []any{int(1), int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), float32(1), float64(1), json.Number("1"), json.Number("1.0"), json.Number("100e-2"), json.Number("0.01e2")}
	for index, value := range indices {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			client, calls, _ := nativePolicyClient(t, nil)
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
			req := nativePolicyRequest(map[string]any{"mode": "explicit", "breakpoints": []any{llm.ModelOptions{"message_index": value, "content_index": value}}})
			req.Messages = append(req.Messages, llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.TextPart("first"), llm.TextPart("selected")}})
			nativePolicyDrain(t, provider.Complete(t.Context(), req))
			if calls.Load() != 1 {
				t.Errorf("calls=%d", calls.Load())
			}
		})
	}
}

func TestNativeCachePolicyRetentionConflict(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, inherited := range []string{"", "in_memory", "24h", "invalid-canary"} {
			for _, request := range []any{nil, "", "in_memory", "24h", false, "invalid-canary"} {
				client, calls, _ := nativePolicyClient(t, nil)
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(responses), openai.WithPromptCache("key-canary", inherited))
				req := nativePolicyRequest(map[string]any{"mode": "implicit"})
				req.Options["prompt_cache_retention"] = request
				nativePolicyWantError(t, provider.Complete(t.Context(), req), openai.ErrInvalidCachePolicy)
				if calls.Load() != 0 {
					t.Error("conflict reached HTTP")
				}
			}
			if inherited == "in_memory" || inherited == "24h" {
				client, calls, _ := nativePolicyClient(t, nil)
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(responses), openai.WithPromptCache("key-canary", inherited))
				nativePolicyWantError(t, provider.Complete(t.Context(), nativePolicyRequest(map[string]any{"mode": "explicit"})), openai.ErrInvalidCachePolicy)
				if calls.Load() != 0 {
					t.Error("inherited conflict reached HTTP")
				}
			}
		}
	}
}

func TestNativeCachePolicyChatRejects12h(t *testing.T) {
	for _, policy := range []any{
		map[string]any{"mode": "explicit", "ttl": "12h"},
		map[string]any{"mode": "explicit", "breakpoints": []any{map[string]any{"message_index": 0, "content_index": 0, "ttl": "12h"}}},
	} {
		client, calls, _ := nativePolicyClient(t, nil)
		provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
		nativePolicyWantError(t, provider.Complete(t.Context(), nativePolicyRequest(policy)), openai.ErrInvalidCachePolicy)
		if calls.Load() != 0 {
			t.Error("Chat 12h reached HTTP")
		}
	}
}

func TestNativeCachePolicyLazyCancellationBorrowing(t *testing.T) {
	client, calls, closed := nativePolicyClient(t, nil)
	provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
	policy := map[string]any{"mode": "explicit"}
	req := nativePolicyRequest(policy)
	stream := provider.Complete(t.Context(), req)
	if calls.Load() != 0 {
		t.Fatal("Complete is not lazy")
	}
	policy["mode"] = "secret-canary" // Borrowing begins at invocation, not Complete.
	nativePolicyWantError(t, stream, openai.ErrInvalidCachePolicy)
	cause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	nativePolicyWantError(t, provider.Complete(ctx, req), cause)
	if calls.Load() != 0 || closed.Load() != 0 {
		t.Fatal("invalid policy/cancellation reached transport")
	}
	policy["mode"] = "explicit"
	before := req.Options.Clone()
	messages := llm.CloneMessages(req.Messages)
	nativePolicyDrain(t, provider.Complete(t.Context(), req))
	if !reflect.DeepEqual(req.Options, before) || !reflect.DeepEqual(req.Messages, messages) {
		t.Fatal("borrowed request mutated")
	}
}

func TestNativeCachePolicyConcurrentRequestsAndEarlyStop(t *testing.T) {
	client, calls, closed := nativePolicyClient(t, nil)
	provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
	req := nativePolicyRequest(map[string]any{"mode": "implicit", "breakpoints": []any{nativePolicySelector(0, 0)}})
	req.Stream = true
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			yields := 0
			provider.Complete(t.Context(), req)(func(_ llm.CompletionChunk, err error) bool {
				yields++
				if err != nil {
					t.Error(err)
				}
				return false
			})
			if yields != 1 {
				t.Errorf("early stop yields=%d", yields)
			}
		})
	}
	workers.Wait()
	if calls.Load() != 8 || closed.Load() != 8 {
		t.Errorf("calls/closed=%d/%d", calls.Load(), closed.Load())
	}
}

func TestNativeCachePolicyRouteIsolation(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusBadRequest) }))
	t.Cleanup(server.Close)
	client, nativeCalls, _ := nativePolicyClient(t, nil)
	providers := []llm.Provider{
		openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithBaseURL(server.URL+"/v1")),
		openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithBaseURL(server.URL+"/v1"), openai.WithPromptCache("key", "")),
		openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true), openai.WithBaseURL(server.URL+"/v1")),
		openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithDeepSeekChatDialect(true)),
		openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithLiteLLMChat()),
		llamacpp.NewProvider(llamacpp.WithBaseURL(server.URL + "/v1")),
		ollama.NewProvider(ollama.WithBaseURL(server.URL + "/v1")),
		deepseek.NewProvider("test", deepseek.WithBaseURL(server.URL+"/v1")),
	}
	for _, provider := range providers {
		nativePolicyWantError(t, provider.Complete(t.Context(), nativePolicyRequest(map[string]any{"mode": "explicit"})), openai.ErrInvalidCachePolicy)
	}
	if calls.Load() != 0 || nativeCalls.Load() != 0 {
		t.Errorf("unsupported routes reached HTTP: %d/%d", calls.Load(), nativeCalls.Load())
	}
}

func TestNativeCachePolicyEnumDomains(t *testing.T) {
	if (openai.NativeCacheMode{}).IsValid() || (openai.NativeCacheTTL{}).IsValid() {
		t.Fatal("enum zero must be invalid")
	}
	for mode := range openai.NativeCacheModes.All() {
		parsed, err := openai.ParseNativeCacheMode(mode.String())
		if err != nil || parsed != mode {
			t.Errorf("mode canonical roundtrip: %v", err)
		}
	}
	for ttl := range openai.NativeCacheTTLs.All() {
		parsed, err := openai.ParseNativeCacheTTL(ttl.String())
		if err != nil || parsed != ttl {
			t.Errorf("ttl canonical roundtrip: %v", err)
		}
	}
}

func TestNativeCachePolicyOmissionPreservesLegacyDefaults(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, retention := range []string{"", "in_memory", "24h"} {
				client, _, _ := nativePolicyClient(t, func(_ *http.Request, body map[string]any) {
					if _, exists := body["prompt_cache_options"]; exists {
						t.Error("omitted policy gained modern request options")
					}
					if body["prompt_cache_key"] != "legacy-key" {
						t.Error("omitted policy lost legacy key")
					}
					value, exists := body["prompt_cache_retention"]
					if exists != (retention != "") || exists && value != retention {
						t.Errorf("omitted policy changed legacy retention: %#v", value)
					}
					input := nativePolicyInput(t, body, responses)
					content := input[0].(map[string]any)["content"]
					if responses {
						want := []any{map[string]any{"type": "input_text", "text": "user"}}
						if !reflect.DeepEqual(content, want) {
							t.Errorf("omitted policy changed Responses content: %#v", content)
						}
					} else if content != "user" {
						t.Errorf("omitted policy changed Chat content: %#v", content)
					}
				})
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithModel("gpt-5.6"), openai.WithResponsesAPI(responses), openai.WithPromptCache("legacy-key", retention))
				nativePolicyDrain(t, provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "user"}}, Stream: stream}))
			}
		}
	}
}
