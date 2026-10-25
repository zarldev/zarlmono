package engine

import (
	"context"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

// MainModeTarget resolves the configured physical target for Plan or Build mode.
// An absent or explicit active route returns active unchanged. Model-only routes
// overlay the active provider; switching providers starts from that provider's
// default and never carries credentials or endpoint overrides across backends.
func (s *Settings) MainModeTarget(ctx context.Context, plan bool, active ProviderSpec) ProviderSpec {
	providerKey := prefs.KeyMainBuildProvider
	if plan {
		providerKey = prefs.KeyMainPlanProvider
	}
	modelKey, _ := prefs.ModelTargetDefault(providerKey)
	provider, providerSet := s.modeTargetSetting(ctx, providerKey)
	model, modelSet := s.modeTargetSetting(ctx, modelKey)
	if provider == prefs.ActiveModelTarget {
		provider, providerSet = "", false
	}
	modelActive := modelSet && model == prefs.ActiveModelTarget
	if !providerSet && (!modelSet || modelActive) {
		return active
	}

	target := active
	if providerSet && provider != active.Name {
		target = ProviderSpec{Name: provider, Model: s.DefaultModelSelection(provider).Model}
	}
	if modelActive {
		target.Model = active.Model
	} else if modelSet {
		target.Model = model
	}
	target.CodexEffort = s.CodexEffort(ctx, target)
	return target
}

func (s *Settings) modeTargetSetting(ctx context.Context, key string) (string, bool) {
	value, err := s.Svc.GetSetting(ctx, prefs.ScopeEffective, key)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(value.Value), true
}
