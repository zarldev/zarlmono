package openai_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

type optionTransport func(*http.Request) (*http.Response, error)

func (f optionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClientOptionsUseFinalState(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			for _, timeout := range []time.Duration{0, time.Minute} {
				t.Run(strings.Join([]string{map[bool]string{false: "chat", true: "responses"}[responses], map[bool]string{false: "forward", true: "reverse"}[reverse], timeout.String()}, "/"), func(t *testing.T) {
					calls := 0
					client := &http.Client{Timeout: time.Hour, Transport: optionTransport(func(req *http.Request) (*http.Response, error) {
						calls++
						path := "/v1/chat/completions"
						body := "data: [DONE]\n\n"
						if responses {
							path = "/v1/responses"
							body = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n"
						}
						if req.URL.String() != "https://provider.invalid"+path {
							t.Errorf("URL = %s", req.URL)
						}
						deadline, bounded := req.Context().Deadline()
						if timeout == 0 && bounded {
							t.Error("zero timeout retained client deadline")
						}
						if timeout > 0 && (!bounded || time.Until(deadline) > timeout) {
							t.Errorf("deadline = %v, bounded = %v", deadline, bounded)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
					})}
					opts := []options.Option[openai.Provider]{openai.WithHTTPClient(client), openai.WithTimeout(time.Second), openai.WithTimeout(timeout), openai.WithBaseURL("https://provider.invalid/v1")}
					if reverse {
						opts = []options.Option[openai.Provider]{openai.WithBaseURL("https://provider.invalid/v1"), openai.WithTimeout(time.Second), openai.WithTimeout(timeout), openai.WithHTTPClient(client)}
					}
					if responses {
						opts = append([]options.Option[openai.Provider]{openai.WithResponsesAPI(true)}, opts...)
					}
					opts = append(opts, openai.WithModel("gpt-5.6"))
					provider := openai.NewProvider("test-key", opts...)
					for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Stream: true}) {
						if err != nil {
							t.Fatalf("Complete: %v", err)
						}
					}
					if calls != 1 {
						t.Errorf("transport calls = %d, want 1", calls)
					}
					if client.Timeout != time.Hour {
						t.Errorf("borrowed client timeout mutated: %v", client.Timeout)
					}
				})
			}
		}
	}
}
