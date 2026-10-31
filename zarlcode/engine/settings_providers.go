package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	backends "github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
)

// ActiveProvider resolves the active provider/model pair from settings, falling
// back to fb (caller's env-derived defaults). Provider and model inheritance is
// paired by scope so an inherited model cannot cross onto a different provider.
// The env BaseURL/APIKey overrides only apply to the fallback provider itself;
// for any other backend they'd be wrong (e.g. a local llama.cpp URL leaking onto
// openai), so other providers resolve those through the registry chain.
func (s *Settings) ActiveProvider(ctx context.Context, fb ProviderSpec) ProviderSpec {
	name := s.resolveProvider(ctx, fb.Name)
	model := fb.Model
	if name != fb.Name {
		model = s.DefaultModelSelection(name).Model
	}
	if s != nil && s.Svc != nil {
		read := func(scope prefs.Scope, key string) (prefs.SettingValue, bool) {
			value, err := s.Svc.GetSetting(ctx, scope, key)
			return value, err == nil && value.Value != ""
		}
		workspaceProvider, hasWorkspaceProvider := read(prefs.ScopeWorkspace, prefs.KeyProvider)
		globalProvider, hasGlobalProvider := read(prefs.ScopeGlobal, prefs.KeyProvider)
		workspaceModel, hasWorkspaceModel := read(prefs.ScopeWorkspace, prefs.KeyModel)
		globalModel, hasGlobalModel := read(prefs.ScopeGlobal, prefs.KeyModel)
		switch {
		case hasWorkspaceProvider && workspaceProvider.Value == name:
			switch {
			case hasWorkspaceModel:
				model = workspaceModel.Value
			case hasGlobalProvider && globalProvider.Value == name && hasGlobalModel:
				model = globalModel.Value
			case !hasGlobalProvider && name == fb.Name && hasGlobalModel:
				model = globalModel.Value
			}
		case !hasWorkspaceProvider && hasGlobalProvider && globalProvider.Value == name:
			if hasWorkspaceModel {
				model = workspaceModel.Value
			} else if hasGlobalModel {
				model = globalModel.Value
			}
		case !hasWorkspaceProvider && !hasGlobalProvider && name == fb.Name:
			// Preserve legacy model-only settings when the effective provider is
			// still the caller's fallback route.
			if hasWorkspaceModel {
				model = workspaceModel.Value
			} else if hasGlobalModel {
				model = globalModel.Value
			}
		}
	}
	effort := fb.CodexEffort
	if effort == "" {
		effort = prefs.DefaultCodexEffort
	}
	spec := ProviderSpec{
		Name:        name,
		Model:       model,
		CodexEffort: s.setting(ctx, prefs.KeyCodexEffort, effort),
	}
	if spec.Name == fb.Name {
		spec.BaseURL = fb.BaseURL
		spec.APIKey = fb.APIKey
	}
	spec.CodexEffort = validCodexEffort(spec, spec.CodexEffort)
	return spec
}

// resolveProvider returns the configured provider, but refuses to let the
// subprocess-spawning claude-code backend be a *default*: it's honoured only
// when explicitly pinned to this workspace, never when merely inherited from
// the global scope (e.g. carried over from the v1 shell on the shared
// state.db). This stops zarlcode-v2 from auto-running the `claude` CLI on
// launch just because v1 had it set; pick it for the workspace to use it.
func (s *Settings) resolveProvider(ctx context.Context, def string) string {
	if s == nil || s.Svc == nil {
		return def
	}
	sv, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, prefs.KeyProvider)
	if err != nil || sv.Value == "" {
		return def
	}
	if id, _ := llm.ParseLLMProvider(sv.Value); id == backends.NameClaudeCode && sv.Source != prefs.ScopeWorkspace {
		slog.WarnContext(ctx, "ignoring inherited claude-code default; using local default — pin claude-code to this workspace to use it",
			"default", def, "source", sv.Source.String())
		return def
	}
	return sv.Value
}

// BuildActive resolves the active provider (settings over fb) and builds
// it, covering every backend method (registry for API-key providers,
// vault-backed token source for OAuth). Returns the built provider plus the
// resolved spec so the caller can label the UI with the real model.
func (s *Settings) BuildActive(ctx context.Context, fb ProviderSpec) (llm.Provider, ProviderSpec, error) {
	spec := s.ActiveProvider(ctx, fb)
	prov, err := BuildProvider(ctx, s.Registry, s.Svc, spec)
	return prov, spec, err
}

// ContextWindow resolves the compaction budget for the active provider.
// The ChatGPT-account Codex backend advertises model caps from /codex/models,
// including auto_compact_token_limit. Prefer that live value when OAuth is
// available; fall back to the registry's static table when the probe fails so
// startup and provider switching never block on it.
func (s *Settings) ContextWindow(ctx context.Context, spec ProviderSpec) int {
	// Explicit registry context caps take priority before the account probe.
	if s != nil && s.Registry != nil {
		if def, err := s.Registry.Parse(spec.Name); err == nil && def.ContextWindow > 0 {
			return def.ContextWindow
		}
	}
	if id, _ := llm.ParseLLMProvider(spec.Name); s != nil && s.Svc != nil && id == backends.NameOpenAICodex {
		// Codex construction uses its default account endpoint; the metadata probe must use the same endpoint, not spec/registry overrides.
		if cw, err := openaicodex.FetchContextWindow(ctx, codex.NewTokenSource(s.Svc), "", spec.Model); err == nil && cw > 0 {
			return cw
		}
	}
	if s == nil || s.Registry == nil {
		return 0
	}
	return s.Registry.ResolveContextWindow(ctx, spec.Name, spec.BaseURL, spec.Model)
}

// ValidateCodexModel checks an OAuth Codex model against the account's live
// catalogue. If a persisted model disappeared, it selects and persists the
// first supported model so subsequent launches do not repeat the failure.
// Other providers and catalogue probe failures leave the selection unchanged.
func (s *Settings) ValidateCodexModel(ctx context.Context, spec ProviderSpec) (ProviderSpec, bool, error) {
	if s == nil || s.Svc == nil {
		return spec, false, nil
	}
	id, _ := llm.ParseLLMProvider(spec.Name)
	if id != backends.NameOpenAICodex {
		return spec, false, nil
	}
	models, err := openaicodex.FetchModels(ctx, codex.NewTokenSource(s.Svc), spec.BaseURL)
	if err != nil {
		return spec, false, err
	}
	for _, model := range models {
		if strings.EqualFold(model.ID, spec.Model) {
			return spec, false, nil
		}
	}
	if len(models) == 0 {
		return spec, false, errors.New("codex account returned no supported models")
	}
	spec.Model = models[0].ID
	selection := prefs.ModelSelection{Provider: spec.Name, Model: spec.Model}
	if err := s.Svc.SetModelSelection(ctx, prefs.ScopeWorkspace, selection); err != nil {
		return spec, false, fmt.Errorf("persist supported Codex model: %w", err)
	}
	return spec, true, nil
}

// Theme resolves the configured theme name, or def when unset.
func (s *Settings) Theme(ctx context.Context, def string) string {
	return s.setting(ctx, prefs.KeyTheme, def)
}

// CompactEngine resolves the chosen compaction engine, defaulting to tiered
// (the quiet, no-LLM progressive trimmer) when unset.
func (s *Settings) CompactEngine(ctx context.Context) string {
	return s.setting(ctx, prefs.KeyCompactEngine, "tiered")
}

// CompactorProvider resolves the LLM target for the summary/executive
// compaction engines. Saved overrides win over the built-in compaction route;
// an explicit "(active)" inherits the main provider/model. A build failure
// falls back to the active provider so a misconfigured override never breaks
// compaction.
func (s *Settings) CompactorProvider(ctx context.Context, active llm.Provider, activeModel string) (llm.Provider, string) {
	target := s.ModelTarget(ctx, prefs.KeyCompactProvider)
	cp, cm := target.Name, target.Model
	if cm == "" {
		cm = activeModel
	}
	if cp == "" {
		return active, cm // reuse the active backend, optional model override
	}
	prov, err := BuildProvider(ctx, s.Registry, s.Svc, ProviderSpec{Name: cp, Model: cm})
	if err != nil || prov == nil {
		return active, activeModel
	}
	return prov, cm
}

// DecomposeJudgeProvider resolves the LLM target for the decompose
// guardrail's constrained-verdict judge. Enabled by default, it uses the
// built-in judge route unless saved preferences override it. Explicit "off"
// keeps the deterministic advisory path; "(active)" reuses the main provider.
// A build failure falls back to the active provider so a
// misconfigured override never silently disables a judge the user enabled.
func (s *Settings) DecomposeJudgeProvider(ctx context.Context, active llm.Provider, activeSpec ProviderSpec) llm.Provider {
	if s.setting(ctx, prefs.KeyDecomposeJudge, prefs.DefaultDecomposeJudge) != "on" {
		return nil
	}
	target := s.ModelTarget(ctx, prefs.KeyJudgeProvider)
	jp, jm := target.Name, target.Model
	if jp == "" && jm == "" {
		return active
	}
	spec := activeSpec // model-only override keeps the active backend's URL/key
	if jp != "" {
		spec = ProviderSpec{Name: jp}
	}
	spec.Model = jm
	if jm == "" {
		spec.Model = activeSpec.Model
	}
	prov, err := BuildProvider(ctx, s.Registry, s.Svc, spec)
	if err != nil || prov == nil {
		slog.WarnContext(ctx, "decompose judge override unbuildable; reusing active provider",
			"judge_provider", jp, "judge_model", jm, "err", err)
		return active
	}
	return prov
}

// VerifyLoop resolves the headless verified re-drive configuration: the
// shell command that acts as the verification oracle (verify_tests) and the
// attempt cap (verify_attempts; default 1 = single-shot, loop off). The
// engine arms the loop only when the command is non-empty AND attempts > 1.
func (s *Settings) VerifyLoop(ctx context.Context) (string, int) {
	cmd := strings.TrimSpace(s.setting(ctx, prefs.KeyVerifyTests, ""))
	attempts := s.intSetting(ctx, prefs.KeyVerifyAttempts, 1)
	return cmd, attempts
}

// TextVerbosity resolves supported request-level output detail.
func (s *Settings) TextVerbosity(ctx context.Context, target ProviderSpec) string {
	if !SupportsTextVerbosity(target) {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(s.setting(ctx, prefs.KeyTextVerbosity, "")))
	switch v {
	case "low", "medium", "high":
		return v
	}
	return ""
}

// CodexEffort resolves the persisted Codex reasoning effort when supported.
func (s *Settings) CodexEffort(ctx context.Context, target ProviderSpec) string {
	return validCodexEffort(target, s.setting(ctx, prefs.KeyCodexEffort, prefs.DefaultCodexEffort))
}
func validCodexEffort(target ProviderSpec, raw string) string {
	id, _ := llm.ParseLLMProvider(target.Name)
	if id != backends.NameOpenAICodex {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	for _, x := range openaicodex.EffortVariants(target.Model) {
		if v == x {
			return v
		}
	}
	return ""
}

// SupportsTextVerbosity reports models with wire-level verbosity support.
func SupportsTextVerbosity(target ProviderSpec) bool {
	id, _ := llm.ParseLLMProvider(target.Name)
	if id == backends.NameOpenAICodex {
		return true
	}
	m := strings.ToLower(strings.TrimSpace(target.Model))
	return id == llm.LLMProviders.OPENAI && (m == "gpt-6.1-sol" || m == "gpt-6-astra" || m == "gpt-5.6" || strings.HasPrefix(m, "gpt-5.6-"))
}
