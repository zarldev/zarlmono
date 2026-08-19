// Package exampleclient assembles live LLM clients for runnable examples.
// Scripted clients remain in each example because their deterministic turns are
// part of that example's teaching contract.
package exampleclient

import (
	"context"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// Config selects a live provider using the example CLI's provider, model, and
// optional endpoint override flags.
type Config struct {
	Provider string
	Model    string
	BaseURL  string
}

// Build constructs a live runner client from zkit's provider registry. An
// explicit BaseURL wins over the selected provider's environment override.
func Build(ctx context.Context, cfg Config) (runner.Client, error) {
	name := cfg.Provider
	if name == "" {
		name = "openai"
	}
	provider, err := backends.NewRegistry().BuildWithConfig(ctx, name, backends.BuildConfig{
		Model:   cfg.Model,
		BaseURL: BaseURL(cfg.BaseURL, name),
	})
	if err != nil {
		return nil, err
	}
	return runner.ClientFromProvider(provider), nil
}

// BaseURL resolves an explicit endpoint before the selected provider's environment override.
func BaseURL(value, provider string) string {
	if value != "" {
		return value
	}
	return os.Getenv(baseURLEnv[provider])
}

var baseURLEnv = map[string]string{
	"deepseek": "DEEPSEEK_BASE_URL",
	"llamacpp": "LLAMACPP_BASE_URL",
	"ollama":   "OLLAMA_BASE_URL",
}
