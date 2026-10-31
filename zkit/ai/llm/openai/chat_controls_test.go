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

func TestChatWireControlsAndReasoningUsage(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, want string
		custom                    bool
	}{
		{"gpt5", "gpt-5", "minimal", "minimal", false},
		{"unsupported", "gpt-5", "none", "", false},
		{"gpt6", "gpt-6-sol", "none", "none", false},
		{"astra", "gpt-6-astra", "none", "", false},
		{"compatible", "gpt-5", "low", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":0}}}`)), Request: req}, nil
			})}
			providerOpts := []options.Option[openai.Provider]{openai.WithHTTPClient(client), openai.WithModel(tc.model), openai.WithResponsesAPI(false)}
			if tc.custom {
				providerOpts = append(providerOpts, openai.WithBaseURL("https://compatible.invalid/v1"))
			}
			provider := openai.NewProvider("test", providerOpts...)
			var usage llm.Usage
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_effort": tc.effort, "text_verbosity": "low"}}) {
				if err != nil {
					t.Fatal(err)
				}
				if chunk.UsageReported {
					usage = chunk.Usage
				}
			}
			value, present := body["reasoning_effort"]
			if tc.want == "" && present || tc.want != "" && value != tc.want {
				t.Errorf("effort=%v, want %q", value, tc.want)
			}
			_, verbosity := body["verbosity"]
			if verbosity == tc.custom {
				t.Errorf("verbosity presence=%v", verbosity)
			}
			if !usage.ReasoningTokensReported || usage.ReasoningTokens != 0 || usage.CompletionTokens != 10 {
				t.Errorf("usage=%+v", usage)
			}
		})
	}
}
