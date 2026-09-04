package google_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func testClient(t *testing.T, baseURL string) *genai.Client {
	t.Helper()
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL},
	})
	if err != nil {
		t.Fatalf("test client: %v", err)
	}
	return client
}

func TestNewProvider_EmptyKeyWithoutClientErrors(t *testing.T) {
	t.Parallel()
	if _, err := google.NewProvider(""); !errors.Is(err, llm.ErrInvalidAPIKey) {
		t.Fatalf("err = %v, want ErrInvalidAPIKey", err)
	}
}

func TestNewProvider_InjectedClientSkipsKeyAndConstruction(t *testing.T) {
	assertInjectedClientWorks(t, func(client *genai.Client) (*google.Provider, error) {
		return google.NewProvider("", google.WithClient(client))
	})
}

func TestNewVertexProvider_InjectedClientSkipsADC(t *testing.T) {
	assertInjectedClientWorks(t, func(client *genai.Client) (*google.Provider, error) {
		return google.NewVertexProvider(t.Context(), "some-project", "us-central1", google.WithClient(client))
	})
}

func assertInjectedClientWorks(t *testing.T, construct func(*genai.Client) (*google.Provider, error)) {
	t.Helper()
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requested = true
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer server.Close()

	p, err := construct(testClient(t, server.URL))
	if err != nil {
		t.Fatalf("construct provider: %v", err)
	}
	for _, err := range p.Complete(t.Context(), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	}) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	if !requested {
		t.Fatal("injected client was not used")
	}
}
