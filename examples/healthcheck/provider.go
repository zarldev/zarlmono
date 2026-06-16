package main

import (
	"context"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

type clientConfig struct {
	Provider string
	Model    string
	BaseURL  string
	Scripted bool
}

func buildClient(ctx context.Context, cfg clientConfig) (runner.Client, func(), error) {
	if cfg.Scripted {
		return runnertest.NewClient(defaultScript()), func() {}, nil
	}

	provider, err := buildProvider(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return runner.ClientFromProvider(provider), func() {}, nil
}

// baseURLEnv maps provider names to their endpoint-override env vars,
// layered between the -base-url flag and the definition default.
var baseURLEnv = map[string]string{
	"deepseek": "DEEPSEEK_BASE_URL",
	"llamacpp": "LLAMACPP_BASE_URL",
	"ollama":   "OLLAMA_BASE_URL",
}

// buildProvider resolves cfg against zkit's builtin provider registry:
// definition defaults for model and base URL, API keys via the
// provider-specific env vars, then the generic LLM_API_KEY.
func buildProvider(ctx context.Context, cfg clientConfig) (llm.Provider, error) {
	name := cfg.Provider
	if name == "" {
		name = "openai"
	}
	reg := backends.NewRegistry()
	return reg.BuildWithConfig(ctx, name, backends.BuildConfig{
		Model:   cfg.Model,
		BaseURL: envOrValue(cfg.BaseURL, baseURLEnv[name]),
	})
}

func envOrValue(value, key string) string {
	if value != "" {
		return value
	}
	return os.Getenv(key)
}
