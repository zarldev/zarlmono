package openai_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestReasoningUsagePresenceAcrossOpenAIEndpoints(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, tc := range []struct {
			name, detail string
			reported     bool
			want         int
		}{
			{"absent", "", false, 0},
			{"null", `,"reasoning_tokens":null`, false, 0},
			{"zero", `,"reasoning_tokens":0`, true, 0},
			{"positive", `,"reasoning_tokens":7`, true, 7},
		} {
			t.Run(tc.name, func(t *testing.T) {
				client := &http.Client{Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
					details := `{"unused":0` + tc.detail + `}`
					body := `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15,"completion_tokens_details":` + details + `}}`
					kind := "application/json"
					if responses {
						kind, body = "text/event-stream", "data: "+`{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":10,"total_tokens":15,"output_tokens_details":`+details+`}}}`+"\n\n"
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {kind}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})}
				provider := openai.NewProvider("test", openai.WithModel("gpt-6-sol"), openai.WithHTTPClient(client), openai.WithResponsesAPI(responses))
				var usage llm.Usage
				var seen bool
				for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: responses, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
					if err != nil {
						t.Fatal(err)
					}
					if chunk.UsageReported {
						usage, seen = chunk.Usage, true
					}
				}
				if !seen || usage.ReasoningTokens != tc.want || usage.ReasoningTokensReported != tc.reported || usage.CompletionTokens != 10 {
					t.Errorf("usage=%+v, seen=%v", usage, seen)
				}
			})
		}
	}
}
