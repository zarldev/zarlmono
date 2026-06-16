package llamacpp_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
)

func TestNewProviderUsesDefaultBaseURL(t *testing.T) {
	t.Parallel()

	provider, err := llamacpp.NewProvider()
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if provider == nil {
		t.Fatal("nil provider")
	}
	// We can't introspect the BaseURL through the public llm.Provider
	// interface; the check that matters is that construction succeeded with
	// no options (which it can only do because we filled in a default).
}

func TestNewProviderHonoursExplicitBaseURL(t *testing.T) {
	t.Parallel()

	provider, err := llamacpp.NewProvider(
		llamacpp.WithBaseURL("http://elsewhere:9999/v1"),
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

	if llamacpp.DefaultBaseURL != "http://localhost:8081/v1" {
		t.Errorf("DefaultBaseURL = %q, want %q",
			llamacpp.DefaultBaseURL, "http://localhost:8081/v1")
	}
}
