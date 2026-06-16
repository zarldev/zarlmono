package deepseek_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestNewProviderUsesDefaultBaseURLAndModel(t *testing.T) {
	t.Parallel()

	provider, err := deepseek.NewProvider("test-key")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if provider == nil {
		t.Fatal("nil provider")
	}
	if got := provider.Name(); got != llm.LLMProviders.DEEPSEEK.String() {
		t.Fatalf("provider.Name() = %q, want %q", got, llm.LLMProviders.DEEPSEEK.String())
	}
}

func TestNewProviderRequiresAPIKey(t *testing.T) {
	t.Parallel()

	if _, err := deepseek.NewProvider(""); err == nil {
		t.Fatal("expected API key error")
	}
}

func TestNewProviderHonoursExplicitBaseURLAndModel(t *testing.T) {
	t.Parallel()

	provider, err := deepseek.NewProvider("test-key",
		deepseek.WithBaseURL("http://elsewhere:9999"),
		deepseek.WithModel("deepseek-v4-pro"),
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

	if deepseek.DefaultBaseURL != "https://api.deepseek.com" {
		t.Errorf("DefaultBaseURL = %q, want %q",
			deepseek.DefaultBaseURL, "https://api.deepseek.com")
	}
}
