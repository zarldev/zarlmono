package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// LiveAgent binds one role to an explicitly selected provider and model.
// Provider is borrowed until the live experiment has joined all executions.
type LiveAgent struct {
	Role     string
	Model    string
	Provider llm.Provider
}

func openLiveProviders(ctx context.Context) ([]LiveAgent, *engine.Settings, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve credential scope: %w", err)
	}
	settings, err := engine.OpenSettings(ctx, root, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("open live provider settings: %w", err)
	}
	var agents []LiveAgent
	for _, route := range []struct {
		role string
		spec engine.ProviderSpec
	}{
		{role: "researcher", spec: engine.ProviderSpec{Name: "deepseek", Model: "deepseek-v4-flash"}},
		{role: "implementer", spec: engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6.1-sol", CodexEffort: "low"}},
		{role: "reviewer", spec: engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6-luna", CodexEffort: "low"}},
	} {
		// Check credential availability without exposing the credential or trying
		// another backend. BuildProvider's OAuth authentication is otherwise lazy.
		if _, err := settings.Svc.GetKey(ctx, prefs.ScopeEffective, route.spec.Name); err != nil {
			_ = settings.Close()
			return nil, nil, fmt.Errorf("%s credential unavailable: %w", route.spec.Name, err)
		}
		provider, err := engine.BuildProvider(ctx, settings.Registry, settings.Svc, route.spec)
		if err != nil {
			_ = settings.Close()
			return nil, nil, fmt.Errorf("build %s route: %w", route.role, err)
		}
		agents = append(agents, LiveAgent{Role: route.role, Model: route.spec.Model, Provider: provider})
	}
	return agents, settings, nil
}

func livePreflight(ctx context.Context) error {
	agents, settings, err := openLiveProviders(ctx)
	if err != nil {
		return err
	}
	defer settings.Close()
	for _, agent := range agents {
		fmt.Printf("%s: %s/%s credential available (model access not yet tested)\n", agent.Role, agent.Provider.Name(), agent.Model)
	}
	return nil
}
