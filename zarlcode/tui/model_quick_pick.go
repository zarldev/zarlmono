package tui

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// modelQuickPick is a centered modal with a provider tab bar and a scrollable
// model list for the selected provider. ctrl+e toggles it. tab/←→ switch
// providers; ↑↓/j/k navigate models; enter selects; esc/q dismiss. Typing any
// other key drops into free-text fallback for the current provider.
type modelQuickPick struct {
	providers []string
	provCur   int
	// models caches fetched model lists per provider name.
	models   map[string][]string
	loading  map[string]bool
	current  string // currently active model (pre-selected)
	onPick   func(provider, model string)
	onEffort func(effort string)
	effort   string
	cursor   int
	// free-text fallback.
	fallback      bool
	fallbackValue []rune
	fallbackCur   int
	meta          *modelInfoResolver
}

func newModelQuickPick(providers []string, models map[string][]string, activeProvider, activeModel string, onPick func(provider, model string), settings *engine.Settings) *modelQuickPick {
	onEffort := func(effort string) {
		if settings == nil || settings.Svc == nil {
			return
		}
		ctx := context.Background()
		if effort == "" {
			if err := settings.Svc.DeleteSetting(ctx, prefs.ScopeWorkspace, prefs.KeyCodexEffort); err != nil {
				slog.WarnContext(ctx, "clear reasoning effort", "err", err)
			}
			return
		}
		if err := settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyCodexEffort, effort); err != nil {
			slog.WarnContext(ctx, "persist reasoning effort", "err", err, "effort", effort)
		}
	}
	return newModelQuickPickWithEffort(providers, models, activeProvider, activeModel, currentCodexEffort(settings), onPick, onEffort, settings)
}

func newModelQuickPickWithEffort(providers []string, models map[string][]string, activeProvider, activeModel, activeEffort string, onPick func(provider, model string), onEffort func(string), settings *engine.Settings) *modelQuickPick {
	if models == nil {
		models = make(map[string][]string)
	}
	mp := &modelQuickPick{
		providers: providers,
		models:    models,
		loading:   make(map[string]bool),
		current:   activeModel,
		onPick:    onPick,
		onEffort:  onEffort,
		effort:    activeEffort,
		meta:      newModelInfoResolver(settings),
	}
	for i, p := range providers {
		if p == activeProvider {
			mp.provCur = i
			break
		}
	}
	// Pre-seed cursor to current model if it's in the list.
	if ml, ok := models[activeProvider]; ok {
		for i, m := range ml {
			if m == activeModel {
				mp.cursor = i
				break
			}
		}
		mp.loading[activeProvider] = false
	} else if activeProvider != "" {
		mp.loading[activeProvider] = true
	}
	return mp
}

// setModels populates the model list for a provider. Called by handleModelsMsg.
func (p *modelQuickPick) setModels(provider string, models []string) {
	p.models[provider] = models
	p.loading[provider] = false
}

func currentCodexEffort(settings *engine.Settings) string {
	if settings == nil {
		return ""
	}
	return settings.Setting(context.Background(), prefs.KeyCodexEffort, "")
}

func (p *modelQuickPick) activeProvider() string {
	if p.provCur < 0 || p.provCur >= len(p.providers) {
		return ""
	}
	return p.providers[p.provCur]
}

func (p *modelQuickPick) activeModel() string {
	models := p.activeModels()
	if p.cursor >= 0 && p.cursor < len(models) {
		return models[p.cursor]
	}
	return p.current
}

func (p *modelQuickPick) effortItems(model string) []string {
	items := []string{codexEffortAuto}
	if p.activeProvider() != backends.NameOpenAICodex.String() {
		return items
	}
	if variants := openaicodex.EffortVariants(model); len(variants) > 0 {
		return append(items, variants...)
	}
	return append(items, "low", "medium", "high", "xhigh", "max")
}

func (p *modelQuickPick) cycleEffort(dir int) {
	items := p.effortItems(p.activeModel())
	if len(items) <= 1 {
		return
	}
	cur := codexEffortAuto
	if p.effort != "" {
		cur = p.effort
	}
	i := slices.Index(items, cur)
	if i < 0 {
		i = 0
	}
	i = (i + dir + len(items)) % len(items)
	if items[i] == codexEffortAuto {
		p.effort = ""
	} else {
		p.effort = items[i]
	}
	if p.onEffort != nil {
		p.onEffort(p.effort)
	}
}

func (p *modelQuickPick) activeModels() []string {
	return p.models[p.activeProvider()]
}

func (p *modelQuickPick) isLoading() bool {
	return p.loading[p.activeProvider()]
}

func (p *modelQuickPick) handleKey(msg tea.KeyPressMsg) action {
	if p.fallback {
		return p.handleFallbackKey(msg)
	}
	switch msg.String() {
	case "esc", "ctrl+e", "q":
		return actionClose{}
	// Provider switching.
	case "tab", "right", "l":
		if len(p.providers) > 1 {
			p.provCur = (p.provCur + 1) % len(p.providers)
			p.cursor = 0
			if _, ok := p.models[p.activeProvider()]; !ok {
				p.loading[p.activeProvider()] = true
				return actionFetchModels{provider: p.activeProvider()}
			}
		}
		return actionNone{}
	case "left", "h":
		if len(p.providers) > 1 {
			p.provCur--
			if p.provCur < 0 {
				p.provCur = len(p.providers) - 1
			}
			p.cursor = 0
			if _, ok := p.models[p.activeProvider()]; !ok {
				p.loading[p.activeProvider()] = true
				return actionFetchModels{provider: p.activeProvider()}
			}
		}
		return actionNone{}
	case "[", "shift+tab":
		p.cycleEffort(-1)
		return actionNone{}
	case "]":
		p.cycleEffort(1)
		return actionNone{}
	}
	// Model list navigation.
	models := p.activeModels()
	if len(models) > 0 && !p.isLoading() {
		switch msg.String() {
		case "up", "k":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "j":
			if p.cursor < len(models)-1 {
				p.cursor++
			}
		case "enter", "space", " ":
			if p.cursor >= 0 && p.cursor < len(models) && p.onPick != nil {
				p.onPick(p.activeProvider(), models[p.cursor])
			}
			return actionClose{}
		}
		if msg.Text != "" || msg.Code == tea.KeyBackspace {
			p.fallback = true
			p.fallbackValue = []rune(p.current)
			p.fallbackCur = len(p.fallbackValue)
			return p.handleKey(msg)
		}
		return actionNone{}
	}
	// No models yet (loading or empty): enter free-text on any key.
	if msg.Text != "" || msg.Code == tea.KeyBackspace {
		p.fallback = true
		p.fallbackValue = []rune(p.current)
		p.fallbackCur = len(p.fallbackValue)
		return p.handleKey(msg)
	}
	return actionNone{}
}

func (p *modelQuickPick) handleFallbackKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "ctrl+e", "q":
		return actionClose{}
	case "enter":
		name := strings.TrimSpace(string(p.fallbackValue))
		if name != "" && p.onPick != nil {
			p.onPick(p.activeProvider(), name)
		}
		return actionClose{}
	case "left":
		if p.fallbackCur > 0 {
			p.fallbackCur--
		}
	case "right":
		if p.fallbackCur < len(p.fallbackValue) {
			p.fallbackCur++
		}
	case "backspace":
		if p.fallbackCur > 0 {
			p.fallbackValue = append(p.fallbackValue[:p.fallbackCur-1], p.fallbackValue[p.fallbackCur:]...)
			p.fallbackCur--
		}
	default:
		if msg.Text != "" {
			rs := []rune(msg.Text)
			out := make([]rune, 0, len(p.fallbackValue)+len(rs))
			out = append(out, p.fallbackValue[:p.fallbackCur]...)
			out = append(out, rs...)
			out = append(out, p.fallbackValue[p.fallbackCur:]...)
			p.fallbackValue = out
			p.fallbackCur += len(rs)
		}
	}
	return actionNone{}
}

func (p *modelQuickPick) draw(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if w < 30 || h < 8 {
		return
	}
	boxW := modelQuickPickMinWidth
	if tabW := p.providerTabsWidth() + 4; tabW > boxW {
		boxW = tabW
	}
	if maxW := w - 4; boxW > maxW {
		boxW = maxW
	}
	boxH := min(20, h-2)
	lay, ok := drawDialogPane(scr, area, "model", boxW, boxH, palette.Border, palette.Primary)
	if !ok {
		return
	}
	innerW, innerX := lay.Body.Dx(), lay.Body.Min.X
	bodyY := lay.Body.Min.Y

	topRight := p.activeProvider()
	if p.activeProvider() == backends.NameOpenAICodex.String() {
		effort := codexEffortAuto
		if p.effort != "" {
			effort = p.effort
		}
		topRight += " · reasoning " + effort
	}
	drawPaddedLine(scr, uv.Rect(innerX, lay.Context.Min.Y, innerW, 1), overlayTopBar("model", nil, 0, topRight, innerW))
	drawPaddedLine(scr, uv.Rect(innerX, bodyY, innerW, 1), palette.Border.On(strings.Repeat("─", innerW)))
	bodyY += 1

	p.drawTabs(scr, innerX, bodyY, innerW)
	bodyY += 2

	if p.fallback {
		p.drawFallback(scr, uv.Rect(innerX, bodyY, innerW, lay.Footer.Min.Y-bodyY))
		return
	}

	models := p.activeModels()
	if p.isLoading() {
		line := "  " + palette.Muted.On("fetching models from "+p.activeProvider()+"...")
		drawPaddedLine(scr, uv.Rect(innerX, bodyY, innerW, 1), line)
		return
	}
	if len(models) == 0 {
		label := palette.Muted.On("  no models — type to enter a model name")
		drawPaddedLine(scr, uv.Rect(innerX, bodyY, innerW, 1), label)
		return
	}
	start, end, up, down := listWindow(p.cursor, len(models), lay.Footer.Min.Y-bodyY)
	y := bodyY
	if up {
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Muted.On("  ↑ more"))
		y++
	}
	for i := start; i < end; i++ {
		model := models[i]
		meta := p.meta.summary(p.activeProvider(), model)
		var line string
		if i == p.cursor {
			line = rowLayout(palette.Primary.On("▸ "+model), meta, innerW)
		} else {
			line = rowLayout("  "+palette.Subtle.On(model), meta, innerW)
		}
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), line)
		y++
	}
	if down && y < lay.Footer.Min.Y {
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Muted.On("  ↓ more"))
	}

	hints := []keyHint{{"↑↓", "navigate"}, {"enter", "select"}, {"tab", "provider"}}
	if p.activeProvider() == backends.NameOpenAICodex.String() {
		hints = append(hints, keyHint{"[]", "reasoning"})
	}
	hints = append(hints, keyHint{"esc", "close"})
	hint := keyLegend(hints...)
	drawPaddedLine(scr, uv.Rect(innerX, lay.Footer.Min.Y, innerW, 1), hint)
}

func (p *modelQuickPick) drawTabs(scr uv.Screen, x, y, width int) {
	if len(p.providers) == 0 {
		return
	}
	var parts []string
	for i, name := range p.providers {
		if i == p.provCur {
			parts = append(parts, palette.Primary.On("[ "+name+" ]"))
		} else {
			parts = append(parts, palette.Subtle.On(name))
		}
	}
	drawPaddedLine(scr, uv.Rect(x, y, width, 1), strings.Join(parts, "  "))
}

const modelQuickPickMinWidth = 60

func (p *modelQuickPick) providerTabsWidth() int {
	if len(p.providers) == 0 {
		return 0
	}
	w := 0
	for _, name := range p.providers {
		w += ansi.StringWidth("[ " + name + " ]  ")
	}
	return w
}

func (p *modelQuickPick) drawFallback(scr uv.Screen, area uv.Rectangle) {
	w := area.Dx()
	prov := p.activeProvider()
	label := palette.Muted.On(fmt.Sprintf(" provider: %s  (type model name)", prov))
	drawPaddedLine(scr, uv.Rect(area.Min.X, area.Min.Y, w, 1), label)

	display := string(p.fallbackValue[:p.fallbackCur]) + palette.Primary.On("▏") + string(p.fallbackValue[p.fallbackCur:])
	drawPaddedLine(scr, uv.Rect(area.Min.X, area.Min.Y+1, w, 1), " "+display)

	hint := keyLegend(keyHint{"enter", "apply"}, keyHint{"esc", "cancel"})
	drawPaddedLine(scr, uv.Rect(area.Min.X, area.Min.Y+2, w, 1), hint)
}
