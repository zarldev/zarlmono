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

// ActiveProvider resolves the active ProviderSpec from settings, falling
// back to fb (caller's env-derived defaults) for any unset field. The env
// BaseURL/APIKey overrides only apply to the fallback provider itself; for
// any other backend they'd be wrong (e.g. a local llama.cpp URL leaking onto
// openai), so other providers resolve those through the registry chain.
func (s *Settings) ActiveProvider(ctx context.Context, fb ProviderSpec) ProviderSpec {
	spec := ProviderSpec{
		Name:        s.resolveProvider(ctx, fb.Name),
		Model:       s.setting(ctx, prefs.KeyModel, fb.Model),
		CodexEffort: s.setting(ctx, prefs.KeyCodexEffort, fb.CodexEffort),
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
	if id, _ := llm.ParseLLMProvider(spec.Name); s != nil && s.Svc != nil && id == backends.NameOpenAICodex {
		if cw, err := openaicodex.FetchContextWindow(ctx, codex.NewTokenSource(s.Svc), spec.BaseURL, spec.Model); err == nil && cw > 0 {
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
// compaction engines. The compact_provider / compact_model settings win when
// set (so daily work can run on a cheap model while briefings use a bigger
// one); otherwise it reuses the active provider + model. A build failure
// falls back to the active provider so a misconfigured override never breaks
// compaction.
func (s *Settings) CompactorProvider(ctx context.Context, active llm.Provider, activeModel string) (llm.Provider, string) {
	cp := s.setting(ctx, prefs.KeyCompactProvider, "")
	cm := s.setting(ctx, prefs.KeyCompactModel, "")
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
// guardrail's constrained-verdict judge. It returns nil while decompose_judge
// is off (the default) — the guardrail keeps its deterministic advisory path.
// When on, judge_provider / judge_model override the target the same way the
// compact_* pair does (verdicts want a small fast model); both unset reuses
// the active provider. A build failure falls back to the active provider so a
// misconfigured override never silently disables a judge the user enabled.
func (s *Settings) DecomposeJudgeProvider(ctx context.Context, active llm.Provider, activeSpec ProviderSpec) llm.Provider {
	if s.setting(ctx, prefs.KeyDecomposeJudge, "off") != "on" {
		return nil
	}
	jp := s.setting(ctx, prefs.KeyJudgeProvider, "")
	jm := s.setting(ctx, prefs.KeyJudgeModel, "")
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
	return validCodexEffort(target, s.setting(ctx, prefs.KeyCodexEffort, ""))
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
	return id == llm.LLMProviders.OPENAI && (m == "gpt-6-astra" || m == "gpt-5.6" || strings.HasPrefix(m, "gpt-5.6-"))
}
