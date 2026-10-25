package engine

import (
	"context"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

const speedStandard = "standard"

// SpeedVariants returns the published speed choices for a supported target.
// Account, region, and workspace eligibility are enforced by the backend, not
// inferred here. Each call returns an independent slice; nil means unsupported.
func SpeedVariants(target ProviderSpec) []string {
	id, _ := llm.ParseLLMProvider(target.Name)
	model := strings.ToLower(strings.TrimSpace(target.Model))
	switch id {
	case backends.NameOpenAICodex:
		model = openaicodex.BaseModelFor(model)
	case llm.LLMProviders.OPENAI:
	default:
		return nil
	}
	switch model {
	case "gpt-6-astra":
		return []string{speedStandard, "fast", "ultrafast"}
	case "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6", "gpt-5.5":
		return []string{speedStandard, "fast"}
	default:
		return nil
	}
}

// ServiceTier resolves the selected main-turn speed to its request option.
// Unset, (auto), invalid, and unsupported choices omit the option. Standard
// explicitly overrides project defaults with the API's "default" tier.
func (s *Settings) ServiceTier(ctx context.Context, target ProviderSpec) string {
	value := strings.ToLower(strings.TrimSpace(s.setting(ctx, prefs.KeyServiceTier, "")))
	if !slices.Contains(SpeedVariants(target), value) {
		return ""
	}
	if value == speedStandard {
		return "default"
	}
	return value
}
