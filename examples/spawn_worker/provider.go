package main

import (
	"errors"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// buildRealClient creates a real LLM client from environment.
// Requires OPENAI_API_KEY to be set.
func buildRealClient() (runner.Client, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY not set")
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}

	provider := openai.NewProvider(apiKey, openai.WithModel(model))

	return runner.ClientFromProvider(provider), nil
}
