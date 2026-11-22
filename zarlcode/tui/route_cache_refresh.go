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

// Pending entries are UI-owned values. A saved row is never evidence that the
// running target has been rebuilt with it.
type routeCacheChange struct {
	baseline, latest backends.RouteCachePolicy
	baselineKnown    bool
	failure          string
}

func (m *UI) routeCacheSaved(a actionRouteCacheSaved) {
	if m.routeCaches == nil {
		m.routeCaches = make(map[routeProtocolKey]routeCacheChange)
	}
	change, exists := m.routeCaches[a.key]
	if !exists {
		change.baseline, change.baselineKnown = a.before, a.baselineKnown
	}
	change.latest = a.after
	if change.baselineKnown && change.baseline == change.latest {
		delete(m.routeCaches, a.key)
	} else {
		m.routeCaches[a.key] = change
	}
	if a.baselineKnown && a.before == a.after {
		return
	}
	m.invalidateRoutePolicyBuilds()
}

func (m *UI) invalidateRoutePolicyBuilds() {
	atomic.AddUint64(&m.repointSeq, 1)
	state := &m.modeTarget
	if state.building || state.prepared != nil {
		state.generation++
		state.prepared = nil
		state.building = false
		state.deferred = true
	}
}

func (m *UI) routeCacheKeyForSpec(spec engine.ProviderSpec) (routeProtocolKey, bool) {
	def, err := m.settings.Registry.Parse(spec.Name)
	if err != nil || (def.AdapterType != backends.AdapterTypes.ANTHROPICCOMPATIBLE && def.AdapterType != backends.AdapterTypes.LITELLM) {
		return routeProtocolKey{}, false
	}
	alias := spec.Model
	if alias == "" {
		alias = def.DefaultModel
	}
	return routeProtocolKey{spec.Name, alias}, true
}

// refreshRouteCache uses the same target preparation/admission as the protocol
// editor. One build resolves both saved rows, and either pending edit is kept
// until a successful admission.
func (m *UI) refreshRouteCache() (tea.Cmd, bool) {
	if len(m.routeCaches) == 0 {
		return nil, false
	}
	spec := m.live.RunTarget().Spec
	key, ok := m.routeCacheKeyForSpec(spec)
	if !ok || spec.CachePolicy.Mode != backends.RouteCacheModes.CACHEINHERIT {
		return nil, false
	}
	change, pending := m.routeCaches[key]
	if !pending {
		return nil, false
	}
	if m.liveOperation != nil || m.sessionRetry != nil || m.manualCompaction != nil || m.isSwarmOpen() {
		m.routeCacheRefreshFailed(key, "runtime busy; close settings again to retry")
		return nil, true
	}
	reasoning, window := activeProviderPolicy(m.settings, spec.Name)
	seq := atomic.AddUint64(&m.repointSeq, 1)
	build := m.buildProviderTargetCmd(seq, spec, reasoning, window, prefs.ModelSelection{}, false, nil)
	sessionID := m.session.ID
	protocolChange, protocolPending := m.routeProtocols[key]
	protocolPending = protocolPending && spec.Protocol == llm.ModelProtocols.PROTOCOLUNKNOWN
	return func() tea.Msg {
		rp, _ := build().(providerRepointedMsg)
		rp.spec = spec
		if rp.target != nil {
			target := *rp.target
			target.Spec = spec
			rp.target = &target
		}
		rp.manual = false
		rp.protocolRefresh = true
		rp.cacheRefresh = true
		rp.cacheKey = key
		rp.cacheValue = change.latest
		if protocolPending {
			rp.protocolKey = key
			rp.protocolValue = protocolChange.latest
			rp.protocolPending = true
		}
		rp.sessionID = sessionID
		return rp
	}, true
}

func (m *UI) routeCacheRefreshFailed(key routeProtocolKey, reason string) {
	change, ok := m.routeCaches[key]
	if !ok {
		return
	}
	change.failure = "Policy saved not applied; retry on settings close: " + strconv.Quote(reason)
	m.routeCaches[key] = change
	m.session.SetErrorToast(change.failure)
}
func (m *UI) finishRouteCacheRefresh(rp providerRepointedMsg, err error) {
	if err != nil {
		m.routeCacheRefreshFailed(rp.cacheKey, err.Error())
		return
	}
	if change, ok := m.routeCaches[rp.cacheKey]; ok && change.latest == rp.cacheValue {
		delete(m.routeCaches, rp.cacheKey)
	}
	m.session.SetSuccessToast("Cache policy saved; current provider refreshed")
	if m.modeTarget.deferred {
		m.scheduledModeTargetCmd = m.deferModeTargetQueueLaunch(false)
	}
}
func (m *UI) annotateRouteCacheEditor() {
	d, ok := topProvidersDialog(m)
	if !ok || d.routeCache == nil {
		return
	}
	e := d.routeCache
	if change, pending := m.routeCaches[routeProtocolKey{e.provider, e.alias.text()}]; pending {
		if change.failure != "" {
			e.status = change.failure
		} else {
			e.status = "Policy saved not applied; refresh pending settings close (runtime override still wins)."
		}
	}
}
func (m *UI) routeCacheApplied(spec engine.ProviderSpec) {
	if len(m.routeCaches) == 0 || spec.CachePolicy.Mode != backends.RouteCacheModes.CACHEINHERIT {
		return
	}
	if key, ok := m.routeCacheKeyForSpec(spec); ok {
		delete(m.routeCaches, key)
	}
}
