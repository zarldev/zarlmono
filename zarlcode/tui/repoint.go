package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// repointTimeout bounds the provider rebuild + context-window probe done
// when the active provider changes.
const repointTimeout = 8 * time.Second

// providerRepointedMsg carries the result of an async provider rebuild back
// to the Update loop, where the live runner + cockpit are updated.
type providerRepointedMsg struct {
	prov      llm.Provider
	spec      engine.ProviderSpec
	window    int                  // resolved window applied to the session/compactor
	reasoning llm.ReasoningHistory // applied reasoning policy
	defWindow int                  // the def's declared window (change-detection baseline)
	err       error
}

// activeProviderPolicy resolves the build-affecting definition fields that
// aren't part of ProviderSpec — the reasoning-history policy and the declared
// context window — for the named provider, so maybeRepoint can detect an edit
// to either and rebuild. Defaults (INLINE, 0) when the def can't be parsed.
func activeProviderPolicy(settings *engine.Settings, name string) (llm.ReasoningHistory, int) {
	if settings == nil || settings.Registry == nil {
		return llm.ReasoningHistories.INLINE, 0
	}
	def, err := settings.Registry.Parse(name)
	if err != nil {
		return llm.ReasoningHistories.INLINE, 0
	}
	return def.ReasoningHistory, def.ContextWindow
}

// maybeRepoint re-resolves the active provider when the settings overlay
// closes; if it changed since the runner was last pointed, it rebuilds the
// provider (off the Update loop, since the context-window probe can touch
// the network) and reports the result. Returns nil when there's nothing to
// re-point or nothing changed.
func (m *UI) maybeRepoint() tea.Cmd {
	if m.live == nil || m.settings == nil {
		return nil
	}
	// Refresh the confirm-quit flag (cheap, synchronous).
	m.session.SetConfirmQuit(m.settings.ConfirmQuit(m.appContext()))
	// Run-budget limits are cheap to apply synchronously (no rebuild needed).
	m.applyLimits()
	// Cost basis is cheap to recompute (reads the registry), so a custom-
	// provider price edit takes effect on close without a provider rebuild.
	m.session.ApplyProviderCostBasis(m.session.ActiveProviderSpec())
	fb, prev := m.session.ProviderContext()
	settings := m.settings
	parent := m.live.ParentContext()
	appliedReasoning := m.appliedReasoning
	appliedWindow := m.appliedWindow
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, repointTimeout)
		defer cancel()
		spec := settings.ActiveProvider(ctx, fb)
		reasoning, defWindow := activeProviderPolicy(settings, spec.Name)
		// Rebuild when the spec changed OR a build-affecting definition field
		// did (reasoning policy, declared context window). Those aren't part of
		// ProviderSpec, so a spec-equality check alone would miss an edit and
		// leave a stale provider/window.
		if spec == prev && reasoning == appliedReasoning && defWindow == appliedWindow {
			return nil
		}
		prov, err := engine.BuildProvider(ctx, settings.Registry, settings.Svc, spec)
		if err != nil {
			return providerRepointedMsg{spec: spec, err: err}
		}
		window := settings.ContextWindow(ctx, spec)
		return providerRepointedMsg{prov: prov, spec: spec, window: window, reasoning: reasoning, defWindow: defWindow}
	}
}

// applyLimits pushes the current run-budget settings (reserve, max
// iterations, spawn depth) onto the live runner. Cheap, synchronous —
// called on every settings-overlay close so an edit takes effect on the
// next turn.
func (m *UI) applyLimits() {
	if m.live == nil || m.settings == nil {
		return
	}
	lim := m.settings.Limits(m.appContext())
	m.live.SetLimits(lim.ReserveTokens, lim.MaxIterations, lim.SpawnMaxIterations, lim.SpawnMaxDepth)
	m.SetPressureConfig(m.session.Run.window, lim.ReserveTokens)
}

// handleRepointMsg applies a completed provider switch: hot-swap the live
// runner's target, update the cockpit identity + gauge, and report the result in
// the footer toast. Returns true when it consumed the message.
func (m *UI) handleRepointMsg(msg tea.Msg) bool {
	rp, ok := msg.(providerRepointedMsg)
	if !ok {
		return false
	}
	if rp.err != nil {
		m.session.SetErrorToast("provider switch failed: " + rp.err.Error())
		return true
	}
	if rp.prov == nil || m.live == nil {
		return true
	}
	m.live.SetProviderSpec(rp.prov, rp.spec)
	m.appliedReasoning = rp.reasoning
	m.appliedWindow = rp.defWindow
	m.session.SetActiveProviderSpec(rp.spec)
	// Refresh the cockpit's cost basis for the new backend. Unmetered
	// backends (local / subscription) carry no per-token rate, even when
	// their model name would match the API price table.
	m.session.ApplyProviderCostBasis(rp.spec)
	if rp.window > 0 {
		m.session.SetContextWindow(rp.window)
		m.live.SetContextWindow(rp.window)
		m.SetPressureConfig(rp.window, m.session.Run.pressureReserve)
	}
	m.session.SetSuccessToast("switched to " + rp.spec.Name + " · " + rp.spec.Model)
	return true
}
