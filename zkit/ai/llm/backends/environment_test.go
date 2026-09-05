package backends_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestRegistryIgnoresCredentialEnvironment(t *testing.T) {
	for _, name := range []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "LLM_API_KEY",
	} {
		t.Setenv(name, "ambient-secret")
	}
	for _, provider := range []string{"openai", "anthropic", "deepseek", "gemini"} {
		t.Run(provider, func(t *testing.T) {
			for _, reg := range []*backends.ProviderRegistry{
				backends.NewRegistry(),
				backends.NewRegistry(backends.WithSettingsService(fakeKeyService{})),
			} {
				if _, err := reg.Build(t.Context(), provider, ""); err == nil {
					t.Fatal("ambient credentials satisfied a missing configured key")
				}
			}
			reg := backends.NewRegistry(backends.WithSettingsService(fakeVault{
				keys: map[string]string{provider: "stored-secret"},
			}))
			if _, err := reg.Build(t.Context(), provider, ""); err != nil {
				t.Fatalf("explicit credential source rejected: %v", err)
			}
		})
	}
}

func TestRegistryIgnoresEndpointEnvironment(t *testing.T) {
	for _, name := range []string{"LLAMACPP_BASE_URL", "OLLAMA_BASE_URL", "DEEPSEEK_BASE_URL"} {
		t.Setenv(name, "http://ambient.invalid/v1")
	}
	reg := backends.NewRegistry()
	for _, name := range []string{"llamacpp", "ollama", "deepseek"} {
		def, err := reg.Parse(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := reg.ResolveBaseURL(name, "", ""); got != def.BaseURL {
			t.Errorf("%s default URL = %q, want %q", name, got, def.BaseURL)
		}
		const explicit = "http://configured.test/v1"
		if got := reg.ResolveBaseURL(name, name, explicit); got != explicit {
			t.Errorf("%s explicit URL = %q, want %q", name, got, explicit)
		}
	}
}
