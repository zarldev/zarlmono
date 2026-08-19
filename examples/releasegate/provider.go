package main

import (
	"context"

	"github.com/zarldev/zarlmono/examples/internal/exampleclient"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
)

type clientConfig struct {
	Provider string
	Model    string
	BaseURL  string
	Scripted bool
}

func buildClient(ctx context.Context, cfg clientConfig) (runner.Client, error) {
	if cfg.Scripted {
		return runnertest.NewClient(defaultScript()), nil
	}
	return exampleclient.Build(ctx, exampleclient.Config{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		BaseURL:  cfg.BaseURL,
	})
}
