package tui

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
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
	selection prefs.ModelSelection
	persist   bool
	done      func(error)
	seq       uint64
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
// closes. Persisted settings edits are already committed, so this path only
// rebuilds and applies the runtime target when its effective configuration
// changed.
func (m *UI) maybeRepoint() tea.Cmd {
	if m.live == nil || m.settings == nil {
		return nil
	}
	// Refresh the confirm-quit flag (cheap, synchronous).
	m.session.SetConfirmQuit(m.settings.ConfirmQuit(m.appContext()))
	// Run-budget limits are cheap to apply synchronously (no rebuild needed).
	m.applyLimits()
	m.live.SetWebSearch(configuredWebSearch(m.appContext(), m.settings))
	// Cost basis is cheap to recompute (reads the registry), so a custom-
	// provider price edit takes effect on close without a provider rebuild.
	m.session.ApplyProviderCostBasis(m.session.ActiveProviderSpec())

	fb, prev := m.session.ProviderContext()
	providerMissing := m.live.RunTarget().Provider == nil
	settings := m.settings
	ctx := m.appContext()
	spec := settings.ActiveProvider(ctx, fb)
	reasoning, defWindow := activeProviderPolicy(settings, spec.Name)
	if !providerMissing && spec == prev && reasoning == m.appliedReasoning && defWindow == m.appliedWindow {
		return nil
	}
	seq := atomic.AddUint64(&m.repointSeq, 1)
	return m.buildProviderTargetCmd(seq, spec, reasoning, defWindow, prefs.ModelSelection{}, false, nil)
}

// switchTarget stages an explicit provider/model selection. The provider is
// built before persistence; a build or persistence failure leaves both the live
// target and the prior persisted selection unchanged.
func (m *UI) switchTarget(selection prefs.ModelSelection, done func(error)) tea.Cmd {
	if m.live == nil || m.settings == nil || m.settings.Svc == nil {
		err := errors.New("provider switch unavailable")
		if done != nil {
			done(err)
		}
		m.session.SetErrorToast(err.Error())
		return nil
	}
	fb, _ := m.session.ProviderContext()
	spec := engine.ProviderSpec{Name: selection.Provider, Model: selection.Model}
	if spec.Name == fb.Name {
		spec.BaseURL = fb.BaseURL
		spec.APIKey = fb.APIKey
	}
	spec.CodexEffort = m.settings.CodexEffort(m.appContext(), spec)
	reasoning, defWindow := activeProviderPolicy(m.settings, spec.Name)
	seq := atomic.AddUint64(&m.repointSeq, 1)
	m.session.SetToast("switching to " + providerModelLabel(spec.Name, spec.Model) + "…")
	return m.buildProviderTargetCmd(seq, spec, reasoning, defWindow, selection, true, done)
}

func (m *UI) buildProviderTargetCmd(
	seq uint64,
	spec engine.ProviderSpec,
	reasoning llm.ReasoningHistory,
	defWindow int,
	selection prefs.ModelSelection,
	persist bool,
	done func(error),
) tea.Cmd {
	settings := m.settings
	parent := m.appContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, repointTimeout)
		defer cancel()
		prov, err := engine.BuildProvider(ctx, settings.Registry, settings.Svc, spec)
		if err != nil {
			return providerRepointedMsg{seq: seq, spec: spec, selection: selection, persist: persist, done: done, err: err}
		}
		window := settings.ContextWindow(ctx, spec)
		return providerRepointedMsg{
			seq: seq, prov: prov, spec: spec, window: window,
			reasoning: reasoning, defWindow: defWindow,
			selection: selection, persist: persist, done: done,
		}
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
	// Mirror the compaction mode so the cockpit knows whether to warn on
	// pressure (manual) or stay quiet because the runner auto-compacts.
	m.session.AutoCompact = m.settings.AutoCompact(m.appContext())
}

// handleRepointMsg commits a completed provider switch. Explicit selections
// persist only after provider construction succeeds, then update the live target
// and session together on the Bubble Tea update loop.
func (m *UI) handleRepointMsg(msg tea.Msg) bool {
	rp, ok := msg.(providerRepointedMsg)
	if !ok {
		return false
	}
	if rp.seq != 0 && rp.seq != atomic.LoadUint64(&m.repointSeq) {
		return true
	}
	if rp.err != nil {
		if rp.done != nil {
			rp.done(rp.err)
		}
		m.session.SetErrorToast("provider switch failed: " + rp.err.Error())
		return true
	}
	if rp.prov == nil || m.live == nil {
		err := errors.New("provider switch returned no live provider")
		if rp.done != nil {
			rp.done(err)
		}
		m.session.SetErrorToast(err.Error())
		return true
	}
	if rp.persist {
		if m.settings == nil || m.settings.Svc == nil {
			err := errors.New("provider switch persistence unavailable")
			if rp.done != nil {
				rp.done(err)
			}
			m.session.SetErrorToast(err.Error())
			return true
		}
		if err := m.settings.Svc.SetModelSelection(m.appContext(), prefs.ScopeWorkspace, rp.selection); err != nil {
			if rp.done != nil {
				rp.done(err)
			}
			m.session.SetErrorToast("provider switch persistence: " + err.Error())
			return true
		}
		if m.settings.Registry != nil {
			m.settings.Registry.SetActiveName(rp.spec.Name)
		}
	}
	m.live.ApplyTarget(engine.TargetUpdate{Provider: rp.prov, Spec: rp.spec, Window: rp.window})
	m.appliedReasoning = rp.reasoning
	m.appliedWindow = rp.defWindow
	m.session.SetActiveProviderSpec(rp.spec)
	// Refresh the cockpit's cost basis for the new backend. Unmetered
	// backends (local / subscription) carry no per-token rate, even when
	// their model name would match the API price table.
	m.session.ApplyProviderCostBasis(rp.spec)
	if rp.window > 0 {
		m.session.SetContextWindow(rp.window)
		m.SetPressureConfig(rp.window, m.session.Run.pressureReserve)
	}
	if m.intro != nil && m.intro.setupRequired {
		m.intro.setupRequired = false
		m.intro.setupError = ""
		m.intro.setupBlocked = false
		m.intro.err = ""
		m.intro.provider = rp.spec.Name
		m.intro.model = rp.spec.Model
	}
	if rp.done != nil {
		rp.done(nil)
	}
	m.session.SetSuccessToast("switched to " + providerModelLabel(rp.spec.Name, rp.spec.Model))
	return true
}
