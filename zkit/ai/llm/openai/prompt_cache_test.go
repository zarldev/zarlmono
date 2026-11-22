package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestChatCacheFieldsRespectEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name             string
		custom, explicit bool
		want             string
	}{
		{"hosted", false, false, "session"},
		{"compatible omits", true, false, ""},
		{"compatible opted in", true, true, "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)), Request: req}, nil
			})}
			opts := []options.Option[openai.Provider]{openai.WithModel("gpt-4o-mini"), openai.WithHTTPClient(client), openai.WithResponsesAPI(false)}
			if tc.custom {
				opts = append(opts, openai.WithBaseURL("https://compatible.invalid/v1"))
			}
			if tc.explicit {
				opts = append(opts, openai.WithPromptCache("default", "in_memory"))
			}
			provider := openai.NewProvider("test", opts...)
			for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"prompt_cache_key": "session", "prompt_cache_retention": "24h"}}) {
				if err != nil {
					t.Fatal(err)
				}
			}
			key, present := body["prompt_cache_key"]
			if tc.want == "" && present || tc.want != "" && key != tc.want {
				t.Errorf("cache key=%v, want %q", key, tc.want)
			}
			_, retention := body["prompt_cache_retention"]
			if tc.want != "" && body["prompt_cache_retention"] != "24h" {
				t.Errorf("cache retention=%v, want 24h", body["prompt_cache_retention"])
			}
			if retention != (tc.want != "") {
				t.Errorf("retention presence=%v", retention)
			}
		})
	}
}

func TestPromptCacheRetentionWireDefaultsAndOverrides(t *testing.T) {
	for _, api := range []struct {
		name      string
		responses bool
		model     string
		path      string
	}{
		{"chat", false, "gpt-4o-mini", "/v1/chat/completions"},
		{"responses", true, "gpt-5.6", "/v1/responses"},
	} {
		for _, tc := range []struct {
			name             string
			constructorValue string
			requestValue     string
			want             string
		}{
			{"constructor in_memory", "in_memory", "", "in_memory"},
			{"request in_memory overrides 24h", "24h", "in_memory", "in_memory"},
			{"request 24h overrides in_memory", "in_memory", "24h", "24h"},
		} {
			t.Run(api.name+"/"+tc.name, func(t *testing.T) {
				var body map[string]any
				client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path != api.path {
						t.Errorf("endpoint path = %q, want %q", req.URL.Path, api.path)
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if api.responses {
						fixture := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n"
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(fixture)), Request: req}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)), Request: req}, nil
				})}
				provider := openai.NewProvider("test",
					openai.WithModel(api.model),
					openai.WithHTTPClient(client),
					openai.WithResponsesAPI(api.responses),
					openai.WithPromptCache("default", tc.constructorValue),
				)
				request := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Stream: api.responses}
				if tc.requestValue != "" {
					request.Options = llm.ModelOptions{"prompt_cache_retention": tc.requestValue}
				}
				for _, err := range provider.Complete(t.Context(), request) {
					if err != nil {
						t.Fatal(err)
					}
				}
				if got := body["prompt_cache_retention"]; got != tc.want {
					t.Errorf("wire prompt_cache_retention = %v, want %q", got, tc.want)
				}
			})
		}
	}
}
