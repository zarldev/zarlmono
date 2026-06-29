package google

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"google.golang.org/genai"
)

// testClient builds a real genai client against the Gemini API backend
// with a dummy key — construction is offline; nothing dials until a
// request is made.
func testClient(t *testing.T) *genai.Client {
	t.Helper()
	c, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey:  "test-key",
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("test client: %v", err)
	}
	return c
}

func TestNewProvider_EmptyKeyWithoutClientErrors(t *testing.T) {
	t.Parallel()
	if _, err := NewProvider(""); !errors.Is(err, llm.ErrInvalidAPIKey) {
		t.Fatalf("err = %v, want ErrInvalidAPIKey", err)
	}
}

// An injected client wins outright: no key required, no client built —
// the provider must hold exactly the client it was handed.
func TestNewProvider_InjectedClientSkipsKeyAndConstruction(t *testing.T) {
	t.Parallel()
	injected := testClient(t)
	p, err := NewProvider("", WithClient(injected))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.client != injected {
		t.Fatal("provider did not keep the injected client")
	}
}

// NewVertexProvider with an injected client must not touch ADC — this
// is what keeps the test (and consumers with their own credential
// plumbing) independent of the machine's gcloud state.
func TestNewVertexProvider_InjectedClientSkipsADC(t *testing.T) {
	t.Parallel()
	injected := testClient(t)
	p, err := NewVertexProvider(t.Context(), "some-project", "us-central1", WithClient(injected))
	if err != nil {
		t.Fatalf("NewVertexProvider: %v", err)
	}
	if p.client != injected {
		t.Fatal("provider did not keep the injected client")
	}
	if p.model != defaultModel {
		t.Fatalf("default model = %q", p.model)
	}
}

func TestWithClient_NilIsIgnored(t *testing.T) {
	t.Parallel()
	if _, err := NewProvider("", WithClient(nil)); !errors.Is(err, llm.ErrInvalidAPIKey) {
		t.Fatalf("nil client must not satisfy the key requirement; err = %v", err)
	}
}
