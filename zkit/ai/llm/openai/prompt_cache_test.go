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
				opts = append(opts, openai.WithPromptCache("default", "in-memory"))
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
			if retention != (tc.want != "") {
				t.Errorf("retention presence=%v", retention)
			}
		})
	}
}
