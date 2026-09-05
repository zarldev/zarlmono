package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
	"github.com/zarldev/zarlmono/zkit/zenv"
)

// buildClient constructs the runner.Client the harness drives, selected
// by LLM_PROVIDER:
//
//	openai-codex  — reuse zarlcode's encrypted vault (~/.zarlcode): the
//	                stored ChatGPT OAuth credential drives the openaicodex
//	                provider exactly as the TUI does, auto-refreshing the
//	                token. NO secret in .env — auth lives in the vault.
//	                LLM_MODEL + CODEX_REASONING_EFFORT tune it.
//	(other)       — an OpenAI-compatible provider from OPENAI_API_KEY /
//	                LLM_BASE_URL / LLM_MODEL.
//
// The returned cleanup releases anything opened (the codex path opens the
// state.db); call it on shutdown.
func buildClient(ctx context.Context) (runner.Client, func(), error) {
	if os.Getenv("LLM_PROVIDER") == "openai-codex" {
		return buildCodexClient(ctx)
	}
	opts := []options.Option[openai.Provider]{}
	if baseURL := os.Getenv("LLM_BASE_URL"); baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if model := zenv.String("LLM_MODEL", "gpt-4o"); model != "" {
		opts = append(opts, openai.WithModel(model))
	}
	prov, err := openai.NewProvider(os.Getenv("OPENAI_API_KEY"), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("openai provider: %w", err)
	}
	return runner.ClientFromProvider(prov), func() {}, nil
}

// buildCodexClient wires the openaicodex provider against zarlcode's
// vault — the same (store, vault, token-source) path the TUI uses, so
// this demo authenticates with the credential you already logged in via
// `zarlcode keys oauth openai-codex`. The OAuth token never touches
// .env; it's decrypted from ~/.zarlcode/state.db at request time and
// refreshed automatically.
func buildCodexClient(ctx context.Context) (runner.Client, func(), error) {
	dir, err := db.DefaultDir()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve state directory: %w", err)
	}
	store, err := db.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("open state.db: %w", err)
	}
	// Database protection policy controls whether this interactive demo prompts.
	svc := prefs.NewService(store, nil, "")
	hasProtectedRows, err := svc.HasVaultBackedKeys(ctx)
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	mode, err := svc.CredentialProtection(ctx)
	if err != nil {
		_ = store.Close()
		return nil, nil, fmt.Errorf("read credential protection: %w", err)
	}
	if hasProtectedRows || mode == prefs.CredentialProtectionPassphrase {
		v, err := vault.Open(dir, vault.TerminalPassphrase)
		if err != nil {
			_ = store.Close()
			return nil, nil, fmt.Errorf("open vault: %w", err)
		}
		svc.SetVault(v)
	}
	tokens := codex.NewTokenSource(svc)

	opts := []options.Option[openaicodex.Provider]{
		openaicodex.WithModel(zenv.String("LLM_MODEL", "gpt-5.5")),
	}
	if effort := os.Getenv("CODEX_REASONING_EFFORT"); effort != "" {
		opts = append(opts, openaicodex.WithDefaultReasoningEffort(effort))
	}
	prov, err := openaicodex.NewProvider(tokens, opts...)
	if err != nil {
		_ = store.Close()
		return nil, nil, fmt.Errorf("codex provider: %w", err)
	}
	return runner.ClientFromProvider(prov), func() { _ = store.Close() }, nil
}
