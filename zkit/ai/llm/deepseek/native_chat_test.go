package deepseek_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

type nativeChatRequest struct {
	Model    string          `json:"model"`
	Thinking json.RawMessage `json:"thinking"`
	Effort   json.RawMessage `json:"reasoning_effort"`
}

func nativeChatServer(t *testing.T, streaming bool, usage string) (*httptest.Server, <-chan nativeChatRequest, *atomic.Int64) {
	t.Helper()
	requests := make(chan nativeChatRequest, 1)
	calls := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request nativeChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- request
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]%s}\n\ndata: [DONE]\n\n", usage)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]%s}`, usage)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests, calls
}

func TestNativeDeepSeekChatControls(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, model := range []string{"deepseek-v4-flash", "opaque-deployment-alias"} {
			for _, tc := range []struct {
				name         string
				enabled      bool
				effort, want string
			}{
				{name: "default"}, {name: "enabled", enabled: true},
				{name: "none", effort: "none", want: "none"},
				{name: "enabled-none", enabled: true, effort: "none", want: "none"},
				{name: "low", effort: "low", want: "low"},
				{name: "high", effort: "high", want: "high"},
				{name: "max", effort: "max", want: "max"},
				{name: "minimal", effort: "minimal", want: "low"},
				{name: "medium", effort: "medium", want: "high"},
				{name: "xhigh", effort: "xhigh", want: "high"},
				{name: "trimmed", effort: " low ", want: "low"},
			} {
				t.Run(fmt.Sprintf("stream=%v/%s/%s", streaming, model, tc.name), func(t *testing.T) {
					server, requests, calls := nativeChatServer(t, streaming, "")
					provider := deepseek.NewProvider("test", deepseek.WithBaseURL(server.URL), deepseek.WithModel(model))
					req := llm.CompletionRequest{Stream: streaming, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Thinking: llm.ThinkingConfig{Enabled: tc.enabled}}
					if tc.effort != "" {
						req.Options = llm.ModelOptions{"reasoning_effort": tc.effort}
					}
					before := req.Options.Clone()
					stream := provider.Complete(t.Context(), req)
					if calls.Load() != 0 {
						t.Fatal("Complete dispatched eagerly")
					}
					consume(t, stream)
					wire := <-requests
					var effort string
					if len(wire.Effort) != 0 {
						if err := json.Unmarshal(wire.Effort, &effort); err != nil {
							t.Fatal(err)
						}
					}
					if wire.Model != model || effort != tc.want || (len(wire.Effort) != 0) != (tc.effort != "") {
						t.Fatalf("wire=%+v, want model=%s effort=%s", wire, model, tc.want)
					}
					if tc.enabled {
						var thinking struct {
							Type string `json:"type"`
						}
						if err := json.Unmarshal(wire.Thinking, &thinking); err != nil {
							t.Fatal(err)
						}
						if thinking.Type != "enabled" {
							t.Fatalf("thinking=%s", wire.Thinking)
						}
					} else if len(wire.Thinking) != 0 {
						t.Fatalf("default thinking overridden: %+v", wire.Thinking)
					}
					if !reflect.DeepEqual(req.Options, before) || req.Messages[0].Content != "hi" {
						t.Fatal("borrowed request mutated")
					}
				})
			}
		}
	}
}

func TestNativeDeepSeekCacheUsage(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			name, usage               string
			cached                    int
			reported                  bool
			prompt, completion, total int
		}{
			{name: "absent"}, {name: "null", usage: `,"usage":null`},
			{name: "zero", usage: `,"usage":{}`, reported: true},
			{name: "reported-zero", usage: `,"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_cache_hit_tokens":0}`, reported: true},
			{name: "nested", usage: `,"prompt_tokens_details":{"cached_tokens":60}`, cached: 60},
			{name: "top-level", usage: `,"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":40`, cached: 60},
			{name: "equal", usage: `,"prompt_tokens_details":{"cached_tokens":60},"prompt_cache_hit_tokens":60`, cached: 60},
			{name: "conflict", usage: `,"prompt_tokens_details":{"cached_tokens":30},"prompt_cache_hit_tokens":60`, cached: 30},
			{name: "canonical-zero", usage: `,"prompt_tokens_details":{"cached_tokens":0},"prompt_cache_hit_tokens":60`},
			{name: "null-details", usage: `,"prompt_tokens_details":null,"prompt_cache_hit_tokens":60`, cached: 60},
			{name: "missing-counter", usage: `,"prompt_tokens_details":{},"prompt_cache_hit_tokens":60`, cached: 60},
			{name: "null-counter", usage: `,"prompt_tokens_details":{"cached_tokens":null},"prompt_cache_hit_tokens":60`, cached: 60},
			{name: "null-hit", usage: `,"prompt_cache_hit_tokens":null`},
			{name: "zero-hit", usage: `,"prompt_cache_hit_tokens":0`},
			{name: "negative-hit", usage: `,"prompt_cache_hit_tokens":-1`},
			{name: "fraction-hit", usage: `,"prompt_cache_hit_tokens":1.5`},
			{name: "string-hit", usage: `,"prompt_cache_hit_tokens":"60"`},
			{name: "bool-hit", usage: `,"prompt_cache_hit_tokens":true`},
			{name: "object-hit", usage: `,"prompt_cache_hit_tokens":{}`},
			{name: "overflow-hit", usage: `,"prompt_cache_hit_tokens":9223372036854775808`},
			{name: "fully-cached", usage: `,"prompt_cache_hit_tokens":100`, cached: 100},
			{name: "negative-canonical", usage: `,"prompt_tokens_details":{"cached_tokens":-1},"prompt_cache_hit_tokens":60`},
			{name: "malformed-details", usage: `,"prompt_tokens_details":[],"prompt_cache_hit_tokens":60`},
			{name: "unbounded-hit", usage: `,"prompt_cache_hit_tokens":101`},
			{name: "malformed-canonical", usage: `,"prompt_tokens_details":{"cached_tokens":"bad"},"prompt_cache_hit_tokens":60`},
			{name: "miss-only", usage: `,"prompt_cache_miss_tokens":40`},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, tc.name), func(t *testing.T) {
				usageJSON := tc.usage
				if tc.name != "absent" && tc.name != "null" && tc.name != "zero" && tc.name != "reported-zero" {
					usageJSON = `,"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110` + tc.usage + `}`
					tc.reported, tc.prompt, tc.completion, tc.total = true, 100, 10, 110
				}
				server, _, _ := nativeChatServer(t, streaming, usageJSON)
				provider := deepseek.NewProvider("test", deepseek.WithBaseURL(server.URL))
				var usage llm.Usage
				var reported bool
				for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: streaming, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
					if err != nil {
						t.Fatal(err)
					}
					if chunk.UsageReported {
						usage, reported = chunk.Usage, true
					}
				}
				if reported != tc.reported || usage.CachedTokens != tc.cached || usage.PromptTokens != tc.prompt || usage.CompletionTokens != tc.completion || usage.TotalTokens != tc.total || usage.CacheCreationTokens != 0 {
					t.Fatalf("usage=%+v reported=%v, want cached=%d reported=%v tokens=%d/%d/%d", usage, reported, tc.cached, tc.reported, tc.prompt, tc.completion, tc.total)
				}
			})
		}
	}
}
