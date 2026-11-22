package tui

import (
	"context"
	"errors"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// Build before acquiring admission or publishing any session state. Like exact
// resume, this synchronous transition is bounded by the provider rebuild timeout.
func (m *UI) prepareOrdinarySessionTarget(saved *savedSession) (engine.TargetUpdate, error) {
	if m.live == nil {
		return engine.TargetUpdate{}, errors.New("provider switch unavailable")
	}
	if m.settings.Registry == nil {
		current := m.live.RunTarget()
		if current.Spec.Name != saved.Provider || current.Spec.Model != saved.Model {
			return engine.TargetUpdate{}, errors.New("saved provider/model unavailable")
		}
		return engine.TargetUpdate{Provider: current.Provider, Spec: current.Spec, Window: current.Window, Facts: current.Facts, RateCard: current.RateCard}, nil
	}
	spec := engine.ProviderSpec{Name: saved.Provider, Model: saved.Model}
	fallback, _ := m.session.ProviderContext()
	if spec.Name == fallback.Name {
		spec.Protocol = fallback.Protocol
		spec.CachePolicy = fallback.CachePolicy
		spec.BaseURL, spec.APIKey = fallback.BaseURL, fallback.APIKey
	}
	ctx, cancel := context.WithTimeout(m.appContext(), repointTimeout)
	defer cancel()
	spec.CodexEffort = m.settings.CodexEffort(ctx, spec)
	target, err := m.settings.PrepareTarget(ctx, spec)
	if err != nil {
		return engine.TargetUpdate{}, errors.New("saved provider/model unavailable with current credentials")
	}
	return target, nil
}
