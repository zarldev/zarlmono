package google_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

type recordingTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.roundTrip(request)
}

func TestProviderBorrowsHTTPPolicyAndUsesStableDefault(t *testing.T) {
	var requests int
	transport := &recordingTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		requests++
		if !strings.Contains(request.URL.Path, "/models/gemini-3.8-flash:streamGenerateContent") {
			t.Errorf("request path = %q, want stable default model", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")),
			Request:    request,
		}, nil
	}}
	client := &http.Client{Transport: transport, Timeout: 17 * time.Second}

	provider, err := google.NewProvider("test-key", google.WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("transport requests = %d, want 1", requests)
	}
	if client.Timeout != 17*time.Second || client.Transport != transport {
		t.Fatalf("borrowed client policy was changed: timeout=%s transport=%T", client.Timeout, client.Transport)
	}
}

func TestProviderReportsBillableThoughtTokens(t *testing.T) {
	server := googleStreamServer(t, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":7,"totalTokenCount":15}}`)
	defer server.Close()

	chunks := completeGoogle(t, server.URL, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "think"}}})
	final := chunks[len(chunks)-1]
	if !final.UsageReported {
		t.Fatal("usage not reported")
	}
	if final.Usage.PromptTokens != 5 || final.Usage.CompletionTokens != 10 || final.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v, want prompt=5 completion=10 total=15", final.Usage)
	}
}

func TestProviderMapsThinkingBudget(t *testing.T) {
	var body string
	transport := &recordingTransport{roundTrip: func(request *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(request.Body)
		body = string(data)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")),
			Request:    request,
		}, nil
	}}
	provider, err := google.NewProvider("test-key", google.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	request := llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "think"}},
		Thinking: llm.ThinkingConfig{Enabled: true, BudgetTokens: 321},
	}
	for _, err := range provider.Complete(t.Context(), request) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(body, `"thinkingConfig":{"includeThoughts":true,"thinkingBudget":321}`) {
		t.Fatalf("thinking config not mapped: %s", body)
	}
}
