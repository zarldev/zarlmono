package tui

import (
	"strconv"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// Values stay on the UI loop. Commands carry snapshots, never this map.
type routeProtocolChange struct {
	baseline, latest llm.ModelProtocol
	baselineKnown    bool
	failure          string
}

func (m *UI) routeProtocolSaved(a actionRouteProtocolSaved) {
	if m.routeProtocols == nil {
		m.routeProtocols = make(map[routeProtocolKey]routeProtocolChange)
	}
	change, exists := m.routeProtocols[a.key]
	if !exists {
		change.baseline, change.baselineKnown = effectiveRouteProtocol(a.before), a.baselineKnown
	}
	change.latest = effectiveRouteProtocol(a.after)
	if change.baselineKnown && change.baseline == change.latest {
		delete(m.routeProtocols, a.key)
	} else {
		m.routeProtocols[a.key] = change
	}
	if a.baselineKnown && effectiveRouteProtocol(a.before) == effectiveRouteProtocol(a.after) {
		return
	}
	// No pre-edit manual/settings build may publish old policy, even when
	// its completion is already queued on the Bubble Tea event loop.
	atomic.AddUint64(&m.repointSeq, 1)
	state := &m.modeTarget
	if state.building || state.prepared != nil {
		state.generation++
		state.prepared = nil
		state.building = false
		state.deferred = true
	}
}

func (m *UI) routeProtocolKeyForSpec(spec engine.ProviderSpec) (routeProtocolKey, bool) {
	def, err := m.settings.Registry.Parse(spec.Name)
	if err != nil || def.AdapterType != backends.AdapterTypes.LITELLM {
		return routeProtocolKey{}, false
	}
	alias := spec.Model
	if alias == "" {
		alias = def.DefaultModel
	}
	return routeProtocolKey{spec.Name, alias}, true
}

// refreshRouteProtocol rebuilds the actual active target with unchanged override
// intent. Saving a connection policy is already durable and never rolled back.
func (m *UI) refreshRouteProtocol() (tea.Cmd, bool) {
	if len(m.routeProtocols) == 0 {
		return nil, false
	}
	spec := m.live.RunTarget().Spec
	key, ok := m.routeProtocolKeyForSpec(spec)
	if !ok || spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		return nil, false
	}
	change, pending := m.routeProtocols[key]
	if !pending {
		return nil, false
	}
	if m.liveOperation != nil || m.sessionRetry != nil || m.manualCompaction != nil || m.isSwarmOpen() {
		m.routeProtocolRefreshFailed(key, "runtime busy; close settings again to retry")
		if spec.CachePolicy.Mode == backends.RouteCacheModes.CACHEINHERIT {
			if cacheKey, ok := m.routeCacheKeyForSpec(spec); ok {
				m.routeCacheRefreshFailed(cacheKey, "runtime busy; close settings again to retry")
			}
		}
		return nil, true
	}
	reasoning, defWindow := activeProviderPolicy(m.settings, spec.Name)
	seq := atomic.AddUint64(&m.repointSeq, 1)
	build := m.buildProviderTargetCmd(seq, spec, reasoning, defWindow, prefs.ModelSelection{}, false, nil)
	sessionID := m.session.ID
	cacheKey, cacheOK := m.routeCacheKeyForSpec(spec)
	cacheChange, cachePending := m.routeCaches[cacheKey]
	cachePending = cacheOK && cachePending && spec.CachePolicy.Mode == backends.RouteCacheModes.CACHEINHERIT
	return func() tea.Msg {
		rp, _ := build().(providerRepointedMsg)
		// Preparation resolves facts for the configured default, but a protocol
		// edit must not turn that default into a new explicit model selection.
		rp.spec = spec
		if rp.target != nil {
			target := *rp.target
			target.Spec = spec
			rp.target = &target
		}
		rp.manual = false
		rp.protocolRefresh = true
		rp.protocolKey = key
		rp.protocolValue = change.latest
		rp.protocolPending = true
		if cachePending {
			rp.cacheRefresh = true
			rp.cacheKey = cacheKey
			rp.cacheValue = cacheChange.latest
		}
		rp.sessionID = sessionID
		return rp
	}, true
}

func (m *UI) routeProtocolRefreshFailed(key routeProtocolKey, reason string) {
	change, exists := m.routeProtocols[key]
	if !exists {
		return
	}
	change.failure = "Policy saved; current provider not refreshed: " + strconv.Quote(reason)
	m.routeProtocols[key] = change
	m.session.SetErrorToast(change.failure)
}

func (m *UI) finishRouteProtocolRefresh(rp providerRepointedMsg, err error) {
	if err != nil {
		m.routeProtocolRefreshFailed(rp.protocolKey, err.Error())
		return
	}
	if change, exists := m.routeProtocols[rp.protocolKey]; exists && change.latest == rp.protocolValue {
		delete(m.routeProtocols, rp.protocolKey)
	}
	m.session.SetSuccessToast("Policy saved; current provider refreshed")
	if m.modeTarget.deferred {
		m.scheduledModeTargetCmd = m.deferModeTargetQueueLaunch(false)
	}
}

// Keep a failed application visible on reopen, independently of saved selection.
func (m *UI) annotateRouteProtocolEditor() {
	d, ok := topProvidersDialog(m)
	if !ok || d.routeProtocol == nil {
		return
	}
	e := d.routeProtocol
	if change, pending := m.routeProtocols[routeProtocolKey{e.provider, e.alias.text()}]; pending {
		if change.failure != "" {
			e.status = change.failure
		} else {
			e.status = "Policy saved; runtime application pending settings close (runtime CLI overrides still win)."
		}
	}
}

// A newer admitted build also consumes pending policy for its exact pair. The
// existing repoint/mode generations reject builds prepared before an edit.
func (m *UI) routeProtocolApplied(spec engine.ProviderSpec) {
	if len(m.routeProtocols) == 0 {
		return
	}
	if spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		return
	}
	if key, ok := m.routeProtocolKeyForSpec(spec); ok {
		delete(m.routeProtocols, key)
	}
}
