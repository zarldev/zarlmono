package backends_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func newModelInfoRegistry() *backends.ProviderRegistry {
	return backends.NewRegistry(backends.WithStore(newFakeStore()), backends.WithSettingsService(fakeKeyService{}))
}

func TestRegistryCost(t *testing.T) {
	reg := newModelInfoRegistry()

	if in, out, ok := reg.Cost("anthropic", "claude-opus-4-7"); !ok || in != 0.015 || out != 0.075 {
		t.Errorf("anthropic opus cost = %v/%v ok=%v, want 0.015/0.075 true", in, out, ok)
	}
	if in, _, ok := reg.Cost("gemini", "gemini-2.5-pro"); !ok || in != 0.00125 {
		t.Errorf("gemini 2.5-pro input cost = %v ok=%v, want 0.00125 true", in, ok)
	}
	if _, _, ok := reg.Cost("openai", "gpt-4o-mini"); !ok {
		t.Error("openai gpt-4o-mini should be metered")
	}
	// Local + subscription backends are not metered per token.
	if _, _, ok := reg.Cost("llamacpp", "qwen3"); ok {
		t.Error("llamacpp (local) must not be metered")
	}
	if _, _, ok := reg.Cost("openai-codex", "gpt-5.5"); ok {
		t.Error("openai-codex (subscription) must not be metered")
	}
	if _, _, ok := reg.Cost("claude-code", "opus"); ok {
		t.Error("claude-code (subscription) must not be metered")
	}
}

func TestRegistryCapabilities(t *testing.T) {
	reg := newModelInfoRegistry()

	if !reg.Capabilities("anthropic", "claude-opus-4-7").SupportsThinking {
		t.Error("claude opus 4.x should support thinking")
	}
	if !reg.Capabilities("gemini", "gemini-2.5-pro").SupportsVision {
		t.Error("gemini should support vision")
	}
	if reg.Capabilities("deepseek", "deepseek-chat").SupportsVision {
		t.Error("deepseek is text-only — no vision")
	}
}

func TestRegistryProviderClass(t *testing.T) {
	reg := newModelInfoRegistry()

	if !reg.IsLocal("llamacpp") || !reg.IsLocal("ollama") {
		t.Error("llamacpp/ollama should be local")
	}
	if reg.IsLocal("openai") {
		t.Error("openai is not local")
	}
	if !reg.IsSubscription("openai-codex") || !reg.IsSubscription("claude-code") {
		t.Error("codex / claude-code should be subscription")
	}
	if reg.IsSubscription("anthropic") {
		t.Error("anthropic API is metered, not subscription")
	}
}
