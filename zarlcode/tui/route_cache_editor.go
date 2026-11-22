package tui

import (
	"errors"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

type routeCacheEditor struct {
	provider       string
	alias          composer
	active         engine.ProviderSpec
	activePolicy   backends.RouteCachePolicy
	activeProtocol llm.ModelProtocol
	activeFacts    bool
	defaultAlias   string
	protocol       llm.ModelProtocol
	adapter        backends.AdapterType
	choosing       bool
	cursor, scroll int
	saved          backends.RouteCachePolicy
	readErr        error
	status         string
}
type actionEditRouteCache struct{}

func (actionEditRouteCache) isAction() {}

type actionRouteCacheRead struct{}

func (actionRouteCacheRead) isAction() {}

type actionRouteCacheSaved struct {
	key           routeProtocolKey
	before, after backends.RouteCachePolicy
	baselineKnown bool
}

func (actionRouteCacheSaved) isAction() {}

type routeCacheChoice struct {
	label  string
	policy backends.RouteCachePolicy
}

var cacheClear = routeCacheChoice{label: "Clear saved policy (inherit)"}
var cacheDefault = routeCacheChoice{label: "Save factory default (explicit mask)", policy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDEFAULT}}
var cacheDisabled = routeCacheChoice{label: "Disable generated markers (not server caching)", policy: backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEDISABLED}}

func routeCacheChoices(protocol llm.ModelProtocol, adapter backends.AdapterType) []routeCacheChoice {
	choices := []routeCacheChoice{cacheClear, cacheDefault}
	if adapter != backends.AdapterTypes.ANTHROPICCOMPATIBLE && (adapter != backends.AdapterTypes.LITELLM || (protocol != llm.ModelProtocols.PROTOCOLMESSAGES && protocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS)) {
		return choices
	}
	choices = append(choices, cacheDisabled)
	if protocol == llm.ModelProtocols.PROTOCOLMESSAGES || adapter == backends.AdapterTypes.ANTHROPICCOMPATIBLE {
		for _, pair := range []struct {
			label           string
			static, rolling backends.RouteCacheTTL
		}{
			{"Messages static 5m / rolling 5m", backends.RouteCacheTTLs.CACHETTL5M, backends.RouteCacheTTLs.CACHETTL5M},
			{"Messages static 1h / rolling 5m", backends.RouteCacheTTLs.CACHETTL1H, backends.RouteCacheTTLs.CACHETTL5M},
			{"Messages static 1h / rolling 1h", backends.RouteCacheTTLs.CACHETTL1H, backends.RouteCacheTTLs.CACHETTL1H},
		} {
			choices = append(choices, routeCacheChoice{pair.label, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: pair.static, RollingTTL: pair.rolling}})
		}
	} else if protocol == llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		for _, ttl := range []struct {
			label string
			value backends.RouteCacheTTL
		}{{"5m", backends.RouteCacheTTLs.CACHETTL5M}, {"1h", backends.RouteCacheTTLs.CACHETTL1H}} {
			for _, mask := range []struct {
				label         string
				system, tools bool
			}{{"system", true, false}, {"tools", false, true}, {"system + tools", true, true}} {
				choices = append(choices, routeCacheChoice{"Chat " + mask.label + " static " + ttl.label, backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: ttl.value, System: mask.system, Tools: mask.tools}})
			}
		}
	}
	return choices
}

func (m *UI) editRouteCache() {
	d, ok := topProvidersDialog(m)
	if !ok {
		return
	}
	def := d.cur()
	if def.AdapterType != backends.AdapterTypes.ANTHROPICCOMPATIBLE && def.AdapterType != backends.AdapterTypes.LITELLM {
		return
	}
	_, active := m.session.ProviderContext()
	var facts *engine.TargetFacts
	if m.live != nil {
		target := m.live.RunTarget()
		active, facts = target.Spec, target.Facts
	}
	alias := def.DefaultModel
	if active.Name == def.Name && active.Model != "" {
		alias = active.Model
	}
	d.routeCache = &routeCacheEditor{provider: def.Name, alias: newComposer(alias), active: active, defaultAlias: def.DefaultModel, adapter: def.AdapterType}
	if facts != nil {
		d.routeCache.activePolicy = facts.CachePolicy
		d.routeCache.activeProtocol = facts.SelectedProtocol
		d.routeCache.activeFacts = true
	}
}

func (d *providersDialog) readRouteCache() {
	e := d.routeCache
	e.saved, e.readErr = d.s.Registry.GetRouteCachePolicy(d.ctx, e.provider, e.alias.text())
	if errors.Is(e.readErr, backends.ErrRouteCachePolicyNotFound) {
		e.saved, e.readErr = backends.RouteCachePolicy{}, nil
	}
	def, _ := d.s.Registry.Parse(e.provider)
	e.adapter = def.AdapterType
	e.protocol = llm.ModelProtocols.PROTOCOLUNKNOWN
	e.status = ""
	if def.AdapterType == backends.AdapterTypes.ANTHROPICCOMPATIBLE {
		e.protocol = llm.ModelProtocols.PROTOCOLMESSAGES
	} else {
		e.readErrProtocol(d)
	}
	e.cursor = 0
	for i, c := range e.choices() {
		if c.policy == e.saved {
			e.cursor = i
		}
	}
	e.choosing, e.scroll = true, 0
}

func (e *routeCacheEditor) readErrProtocol(d *providersDialog) {
	p, err := d.s.Registry.GetRouteProtocol(d.ctx, e.provider, e.alias.text())
	switch {
	case err == nil:
		e.protocol = effectiveRouteProtocol(p)
	case errors.Is(err, backends.ErrRouteProtocolNotFound):
		e.protocol = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
	default:
		e.status = "Saved protocol unavailable: " + strconv.Quote(err.Error())
	}
	if e.active.Name == e.provider && e.active.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		alias := e.active.Model
		if alias == "" {
			alias = e.defaultAlias
		}
		if alias == e.alias.text() {
			e.protocol = e.active.Protocol
		}
	}
}

func (d *providersDialog) handleRouteCacheKey(msg tea.KeyPressMsg) action {
	e := d.routeCache
	switch msg.String() {
	case "esc":
		d.routeCache = nil
		return actionNone{}
	case "pgup":
		e.scroll = max(0, e.scroll-5)
		return actionNone{}
	case "pgdown":
		e.scroll += 5
		return actionNone{}
	}
	if !e.choosing {
		switch msg.String() {
		case "enter", "tab":
			d.readRouteCache()
			return actionRouteCacheRead{}
		case "backspace":
			e.alias.backspace()
		case "delete":
			e.alias.deleteForward()
		case "left":
			e.alias.left()
		case "right":
			e.alias.right()
		case "home", "ctrl+a":
			e.alias.cursor = 0
		case "end", "ctrl+e":
			e.alias.cursor = len(e.alias.value)
		case "ctrl+u":
			e.alias = composer{}
		default:
			if msg.Text != "" {
				e.alias.insert(msg.Text)
			}
		}
		return actionNone{}
	}
	choices := e.choices()
	switch msg.String() {
	case "up", "left":
		e.cursor = (e.cursor + len(choices) - 1) % len(choices)
	case "down", "right", "tab":
		e.cursor = (e.cursor + 1) % len(choices)
	case "shift+tab":
		e.choosing, e.scroll = false, 0
	case "enter":
		choice := choices[e.cursor]
		var err error
		if e.cursor == 0 {
			err = d.s.Registry.DeleteRouteCachePolicy(d.ctx, e.provider, e.alias.text())
		} else {
			err = d.s.Registry.SetRouteCachePolicy(d.ctx, e.provider, e.alias.text(), choice.policy)
		}
		if err != nil {
			e.status = "Policy not saved: " + strconv.Quote(err.Error())
			return actionNone{}
		}
		a := actionRouteCacheSaved{routeProtocolKey{e.provider, e.alias.text()}, e.saved, choice.policy, e.readErr == nil}
		e.saved, e.readErr = choice.policy, nil
		e.status = "Policy saved; applies to future builds. Current provider refresh checked on settings close."
		return a
	}
	return actionNone{}
}

func (e *routeCacheEditor) lines(width int) []string {
	lines := []string{"Route cache markers", "Connection policy · shared across workspaces"}
	lines = append(lines, routeProtocolWrap("Request markers do not prove cache storage or a cache hit.", width)...)
	lines = append(lines, routeProtocolWrap("Provider: "+strconv.Quote(e.provider), width)...)
	lines = append(lines, routeProtocolWrap("Alias: "+strconv.Quote(e.alias.text()), width)...)
	if !e.choosing {
		lines = append(lines, routeProtocolWrap("Input: "+strconv.Quote(string(e.alias.value[:e.alias.cursor]))+"▏"+strconv.Quote(string(e.alias.value[e.alias.cursor:])), width)...)
		lines = append(lines, "Edit exact alias; Enter/Tab reads policy.", "Ctrl+U clears input; PgUp/PgDown scroll.")
	} else {
		switch {
		case e.readErr != nil:
			lines = append(lines, routeProtocolWrap("Saved policy unavailable: "+strconv.Quote(e.readErr.Error()), width)...)
		case e.saved.Mode == backends.RouteCacheModes.CACHEINHERIT:
			lines = append(lines, "Not configured; factory default inherited.")
		default:
			lines = append(lines, routeProtocolWrap("Saved policy: "+cachePolicyLabel(e.saved), width)...)
		}
		lines = append(lines, "Resolved protocol for choices: "+e.protocol.String())
		if e.active.Name == e.provider {
			alias := e.active.Model
			if alias == "" {
				alias = e.defaultAlias
			}
			if alias == e.alias.text() {
				if e.active.CachePolicy.Mode != backends.RouteCacheModes.CACHEINHERIT {
					lines = append(lines, routeProtocolWrap("Runtime cache override wins for this run: "+cachePolicyLabel(e.active.CachePolicy), width)...)
				}
				if e.activeFacts {
					lines = append(lines, routeProtocolWrap("Active effective markers: "+cachePolicyLabel(e.activePolicy)+" (protocol "+e.activeProtocol.String()+")", width)...)
				}
				lines = append(lines, routeProtocolWrap("Saved configuration alone does not confirm runtime application.", width)...)
			}
		}
		for i, c := range e.choices() {
			label := "  " + c.label
			if i == e.cursor {
				label = "> " + c.label
			}
			if e.readErr == nil && c.policy == e.saved {
				label += " [saved]"
			}
			lines = append(lines, routeProtocolWrap(label, width)...)
		}
		lines = append(lines, "Enter saves; Shift+Tab edits alias; Esc cancels.")
	}
	if e.status != "" {
		lines = append(lines, routeProtocolWrap(e.status, width)...)
	}
	return lines
}
func cachePolicyLabel(p backends.RouteCachePolicy) string {
	return p.Mode.String() + " (static " + p.StaticTTL.String() + ", rolling " + p.RollingTTL.String() + ", system " + strconv.FormatBool(p.System) + ", tools " + strconv.FormatBool(p.Tools) + ")"
}
func (e *routeCacheEditor) visibleLines(width, height int) []string {
	lines := e.lines(width)
	e.scroll = min(e.scroll, max(0, len(lines)-height))
	return lines[e.scroll:min(len(lines), e.scroll+height)]
}

// choices preserves valid saved default-TTL intent without selecting Clear just
// because the editor's explicit presets use concrete lifetimes. The extra choice
// is offered only when its effective policy matches a supported preset.
func (e *routeCacheEditor) choices() []routeCacheChoice {
	choices := routeCacheChoices(e.protocol, e.adapter)
	if e.readErr != nil {
		return choices
	}
	for _, choice := range choices {
		if choice.policy == e.saved {
			return choices
		}
	}
	effective := e.saved
	if effective.Mode == backends.RouteCacheModes.CACHEEXPLICIT {
		if e.adapter == backends.AdapterTypes.ANTHROPICCOMPATIBLE || e.protocol == llm.ModelProtocols.PROTOCOLMESSAGES {
			if effective.StaticTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT {
				effective.StaticTTL = backends.RouteCacheTTLs.CACHETTL1H
			}
			if effective.RollingTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT {
				effective.RollingTTL = backends.RouteCacheTTLs.CACHETTL5M
			}
		} else if e.protocol == llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS && effective.StaticTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT {
			effective.StaticTTL = backends.RouteCacheTTLs.CACHETTL5M
		}
	}
	for _, choice := range choices {
		if choice.policy == effective {
			return append(choices, routeCacheChoice{label: "Keep current saved policy: " + cachePolicyLabel(e.saved), policy: e.saved})
		}
	}
	return choices
}
