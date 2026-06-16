package ollama_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
)

func TestNewProviderUsesDefaultBaseURL(t *testing.T) {
	t.Parallel()

	provider, err := ollama.NewProvider()
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if provider == nil {
		t.Fatal("nil provider")
	}
}

func TestNewProviderHonoursExplicitBaseURL(t *testing.T) {
	t.Parallel()

	provider, err := ollama.NewProvider(
		ollama.WithBaseURL("http://elsewhere:9999/v1"),
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if provider == nil {
		t.Fatal("nil provider")
	}
}

func TestDefaultBaseURLConstant(t *testing.T) {
	t.Parallel()

	if ollama.DefaultBaseURL != "http://localhost:11434/v1" {
		t.Errorf("DefaultBaseURL = %q, want %q",
			ollama.DefaultBaseURL, "http://localhost:11434/v1")
	}
}
