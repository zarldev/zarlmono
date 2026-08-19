package main

import (
	"context"

	"github.com/zarldev/zarlmono/examples/internal/exampleclient"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

type clientConfig struct {
	Provider string
	Model    string
	BaseURL  string
	Scripted bool
}

func buildClient(ctx context.Context, cfg clientConfig) (runner.Client, error) {
	if cfg.Scripted {
		return NewScriptedClient(), nil
	}
	return exampleclient.Build(ctx, exampleclient.Config{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		BaseURL:  cfg.BaseURL,
	})
}
