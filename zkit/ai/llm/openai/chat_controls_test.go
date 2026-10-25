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

func TestChatExplicitControlsAndReasoningUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			model, effort, want string
			custom              bool
		}{
			{"gpt-5", "minimal", "minimal", false},
			{"gpt-5", "none", "", false},
			{"gpt-5", "max", "", false},
			{"gpt-6-sol", "none", "none", false},
			{"gpt-6-astra", "none", "", false},
			{"gpt-6.1-sol", "none", "", false},
			{"gpt-4o", "low", "", false},
			{"gpt-5", "low", "", true},
		} {
			t.Run(tc.model+tc.effort+strconv.FormatBool(tc.custom)+strconv.FormatBool(stream), func(t *testing.T) {
				var body map[string]any
				client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					response := `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":0}}}`
					kind := "application/json"
					if stream {
						kind = "text/event-stream"
						response = "data: " + response + "\n\ndata: [DONE]\n\n"
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
				})}
				opts := []options.Option[openai.Provider]{openai.WithHTTPClient(client), openai.WithModel(tc.model), openai.WithResponsesAPI(false)}
				if tc.custom {
					opts = append(opts, openai.WithBaseURL("https://compatible.invalid/v1"))
				}
				provider := openai.NewProvider("test", opts...)
				var usage llm.Usage
				for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_effort": tc.effort, "text_verbosity": "low"}}) {
					if err != nil {
						t.Fatal(err)
					}
					if chunk.UsageReported {
						usage = chunk.Usage
					}
				}
				got, present := body["reasoning_effort"]
				if tc.want == "" && present || tc.want != "" && got != tc.want {
					t.Errorf("effort = %v, want %q", got, tc.want)
				}
				_, verbosity := body["verbosity"]
				if verbosity != (!tc.custom && tc.model != "gpt-4o") {
					t.Errorf("verbosity presence = %v", verbosity)
				}
				if _, nested := body["text"]; nested {
					t.Error("Responses text block leaked into Chat")
				}
				if !usage.ReasoningTokensReported || usage.ReasoningTokens != 0 || usage.CompletionTokens != 10 {
					t.Errorf("usage = %+v", usage)
				}
			})
		}
	}
}
