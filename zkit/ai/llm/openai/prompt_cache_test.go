package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestChatPromptCacheOptionsRespectEndpointSupport(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, baseURL, defaultKey, defaultTTL, wantKey, wantTTL string
			explicit                                                bool
			request                                                 llm.ModelOptions
		}{
			{name: "hosted request", request: llm.ModelOptions{"prompt_cache_key": "session", "prompt_cache_retention": "24h"}, wantKey: "session", wantTTL: "24h"},
			{name: "hosted defaults", explicit: true, defaultKey: "default", defaultTTL: "in-memory", wantKey: "default", wantTTL: "in-memory"},
			{name: "request overrides", explicit: true, defaultKey: "default", defaultTTL: "24h", request: llm.ModelOptions{"prompt_cache_key": "session", "prompt_cache_retention": "in-memory"}, wantKey: "session", wantTTL: "in-memory"},
			{name: "compatible default omits", baseURL: "https://local.invalid/v1", request: llm.ModelOptions{"prompt_cache_key": "session", "prompt_cache_retention": "24h"}},
			{name: "compatible explicit", baseURL: "https://proxy.invalid/v1", explicit: true, request: llm.ModelOptions{"prompt_cache_key": "session", "prompt_cache_retention": "24h"}, wantKey: "session", wantTTL: "24h"},
			{name: "invalid values omitted", request: llm.ModelOptions{"prompt_cache_key": 42, "prompt_cache_retention": "forever"}},
			{name: "invalid retention preserves default", explicit: true, defaultKey: "default", defaultTTL: "in-memory", request: llm.ModelOptions{"prompt_cache_retention": "forever"}, wantKey: "default", wantTTL: "in-memory"},
		} {
			t.Run(tc.name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				var body map[string]any
				client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path != "/v1/chat/completions" {
						t.Errorf("path = %s", req.URL.Path)
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					response := `{"id":"chat-1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
					contentType := "application/json"
					if stream {
						contentType, response = "text/event-stream", "data: [DONE]\n\n"
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
				})}
				opts := []options.Option[openai.Provider]{openai.WithHTTPClient(client), openai.WithModel("gpt-4o-mini")}
				if tc.baseURL != "" {
					opts = append(opts, openai.WithBaseURL(tc.baseURL))
				}
				if tc.explicit {
					opts = append(opts, openai.WithPromptCache(tc.defaultKey, tc.defaultTTL))
				}
				provider := openai.NewProvider("test", opts...)
				for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: tc.request}) {
					if err != nil {
						t.Fatal(err)
					}
				}
				for field, want := range map[string]string{"prompt_cache_key": tc.wantKey, "prompt_cache_retention": tc.wantTTL} {
					got, present := body[field]
					if (want == "" && present) || (want != "" && got != want) {
						t.Errorf("%s = %v (present=%v), want %q", field, got, present, want)
					}
				}
			})
		}
	}
}
