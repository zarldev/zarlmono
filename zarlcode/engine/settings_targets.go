package engine

import (
	"context"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

// ModelTarget resolves a compaction, judge, or per-mode spawn route. The built-in
// pair applies only when neither preference exists. Partial saved overrides keep
// their existing active-target inheritance; explicit "(active)" values opt out of
// the built-in route without deleting preferences or inheriting a global pin.
func (s *Settings) ModelTarget(ctx context.Context, providerKey string) ProviderSpec {
	modelKey, def := prefs.ModelTargetDefault(providerKey)
	if s == nil || s.Svc == nil {
		return ProviderSpec{Name: def.Provider, Model: def.Model}
	}
	provider, providerErr := s.Svc.GetSetting(ctx, prefs.ScopeEffective, providerKey)
	model, modelErr := s.Svc.GetSetting(ctx, prefs.ScopeEffective, modelKey)
	if providerErr != nil && modelErr != nil {
		return ProviderSpec{Name: def.Provider, Model: def.Model}
	}
	return ProviderSpec{Name: inheritedTargetValue(provider.Value), Model: inheritedTargetValue(model.Value)}
}

func inheritedTargetValue(value string) string {
	value = strings.TrimSpace(value)
	if value == prefs.ActiveModelTarget {
		return ""
	}
	return value
}
