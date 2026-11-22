package tui

import (
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// A zero protocol represents an absent saved policy, never a stored value.
var routeProtocolChoices = [...]struct {
	label    string
	protocol llm.ModelProtocol
}{
	{label: "Default / clear saved policy (Chat Completions)"},
	{label: "Chat Completions", protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
	{label: "Responses", protocol: llm.ModelProtocols.PROTOCOLRESPONSES},
	{label: "Anthropic Messages", protocol: llm.ModelProtocols.PROTOCOLMESSAGES},
	{label: "GenerateContent", protocol: llm.ModelProtocols.PROTOCOLGENERATECONTENT},
}

type routeProtocolKey struct{ provider, alias string }

type routeProtocolEditor struct {
	provider     string
	alias        composer
	active       engine.ProviderSpec
	defaultAlias string
	choosing     bool
	cursor       int
	saved        llm.ModelProtocol
	readErr      error
	status       string
	scroll       int
}

type actionRouteProtocolRead struct{}

func (actionRouteProtocolRead) isAction() {}

type actionEditRouteProtocol struct{}

func (actionEditRouteProtocol) isAction() {}

type actionRouteProtocolSaved struct {
	key           routeProtocolKey
	before, after llm.ModelProtocol
	baselineKnown bool
}

func (actionRouteProtocolSaved) isAction() {}

func (m *UI) editRouteProtocol() {
	d, ok := topProvidersDialog(m)
	if !ok {
		return
	}
	def := d.cur()
	if def.AdapterType != backends.AdapterTypes.LITELLM {
		return
	}
	_, active := m.session.ProviderContext()
	if m.live != nil {
		active = m.live.RunTarget().Spec
	}
	alias := def.DefaultModel
	if active.Name == def.Name && active.Model != "" {
		alias = active.Model
	}
	d.routeProtocol = &routeProtocolEditor{provider: def.Name, alias: newComposer(alias), active: active, defaultAlias: def.DefaultModel}
}

func effectiveRouteProtocol(protocol llm.ModelProtocol) llm.ModelProtocol {
	if protocol == llm.ModelProtocols.PROTOCOLUNKNOWN {
		return llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
	}
	return protocol
}

func (d *providersDialog) readRouteProtocol() {
	e := d.routeProtocol
	e.saved, e.readErr = d.s.Registry.GetRouteProtocol(d.ctx, e.provider, e.alias.text())
	if errors.Is(e.readErr, backends.ErrRouteProtocolNotFound) {
		e.saved, e.readErr = llm.ModelProtocols.PROTOCOLUNKNOWN, nil
	}
	e.cursor = 0
	if e.readErr == nil {
		for i, choice := range routeProtocolChoices {
			if choice.protocol == e.saved {
				e.cursor = i
			}
		}
	}
	e.choosing, e.scroll, e.status = true, 0, ""
}

func (d *providersDialog) handleRouteProtocolKey(msg tea.KeyPressMsg) action {
	e := d.routeProtocol
	if msg.String() == "esc" {
		d.routeProtocol = nil
		return actionNone{}
	}
	if msg.String() == "pgup" {
		e.scroll = max(0, e.scroll-5)
		return actionNone{}
	}
	if msg.String() == "pgdown" {
		e.scroll += 5
		return actionNone{}
	}
	if !e.choosing {
		switch msg.String() {
		case "enter", "tab":
			d.readRouteProtocol()
			return actionRouteProtocolRead{}
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
	switch msg.String() {
	case "up", "left":
		e.cursor = (e.cursor + len(routeProtocolChoices) - 1) % len(routeProtocolChoices)
	case "down", "right", "tab":
		e.cursor = (e.cursor + 1) % len(routeProtocolChoices)
	case "shift+tab":
		e.choosing, e.scroll = false, 0
	case "enter":
		choice := routeProtocolChoices[e.cursor]
		var err error
		if e.cursor == 0 {
			err = d.s.Registry.DeleteRouteProtocol(d.ctx, e.provider, e.alias.text())
		} else {
			err = d.s.Registry.SetRouteProtocol(d.ctx, e.provider, e.alias.text(), choice.protocol)
		}
		if err != nil {
			e.status = "Policy not saved: " + strconv.Quote(err.Error())
			return actionNone{}
		}
		a := actionRouteProtocolSaved{key: routeProtocolKey{e.provider, e.alias.text()}, before: e.saved, after: choice.protocol, baselineKnown: e.readErr == nil}
		e.saved, e.readErr = choice.protocol, nil
		e.status = "Policy saved; applies to future builds. Current provider refresh is checked on settings close."
		return a
	}
	return actionNone{}
}

func (e *routeProtocolEditor) lines(width int) []string {
	lines := []string{"Route protocol", "Connection policy · shared across workspaces"}
	lines = append(lines, routeProtocolWrap("Provider: "+strconv.Quote(e.provider), width)...)
	lines = append(lines, routeProtocolWrap("Alias: "+strconv.Quote(e.alias.text()), width)...)
	if e.active.Name == e.provider && e.active.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN {
		alias := e.active.Model
		if alias == "" {
			alias = e.defaultAlias
		}
		if alias == e.alias.text() {
			lines = append(lines, routeProtocolWrap("Runtime CLI protocol override wins for this run: "+e.active.Protocol.String()+" (never saved here)", width)...)
		}
	}
	if !e.choosing {
		lines = append(lines, routeProtocolWrap("Input: "+strconv.Quote(string(e.alias.value[:e.alias.cursor]))+"▏"+strconv.Quote(string(e.alias.value[e.alias.cursor:])), width)...)
		lines = append(lines, "Edit exact alias; Enter/Tab reads policy.", "Ctrl+U clears input; PgUp/PgDown scroll.")
	} else {
		switch {
		case errors.Is(e.readErr, backends.ErrNoStore):
			lines = append(lines, "Durable storage unavailable; saved policy unknown.")
		case e.readErr != nil:
			lines = append(lines, routeProtocolWrap("Saved policy unavailable: "+strconv.Quote(e.readErr.Error()), width)...)
		case e.saved == llm.ModelProtocols.PROTOCOLUNKNOWN:
			lines = append(lines, "Not configured; default Chat Completions.")
		default:
			lines = append(lines, "Saved policy: "+e.saved.String())
		}
		for i, choice := range routeProtocolChoices {
			label := "  " + choice.label
			if i == e.cursor {
				label = "> " + choice.label
			}
			if e.readErr == nil && choice.protocol == e.saved {
				if i == 0 {
					label += " [not configured]"
				} else {
					label += " [saved]"
				}
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

func (e *routeProtocolEditor) visibleLines(width, height int) []string {
	lines := e.lines(width)
	e.scroll = min(e.scroll, max(0, len(lines)-height))
	return lines[e.scroll:min(len(lines), e.scroll+height)]
}

// Quote before wrapping so opaque keys cannot inject terminal control sequences.
// Display-cell wrapping keeps wide Unicode aliases accessible without truncation.
func routeProtocolWrap(text string, width int) []string {
	return strings.Split(ansi.Hardwrap(text, max(1, width), true), "\n")
}
