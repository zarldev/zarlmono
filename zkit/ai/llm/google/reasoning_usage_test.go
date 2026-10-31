package google_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestReasoningUsageIsSubtotalWithConservativePresence(t *testing.T) {
	for _, tc := range []struct {
		name, thoughts string
		want           int
	}{
		{"absent", "", 0},
		{"zero lacks SDK presence", `,"thoughtsTokenCount":0`, 0},
		{"positive", `,"thoughtsTokenCount":7`, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
				body := "data: " + `{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3` + tc.thoughts + `}}` + "\n\n"
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}}
			provider, err := google.NewProvider("test", google.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			var usage llm.Usage
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
				if err != nil {
					t.Fatal(err)
				}
				if chunk.UsageReported {
					usage = chunk.Usage
				}
			}
			if usage.ReasoningTokens != tc.want || usage.ReasoningTokensReported != (tc.want > 0) || usage.CompletionTokens != 3+tc.want {
				t.Errorf("usage=%+v", usage)
			}
		})
	}
}
