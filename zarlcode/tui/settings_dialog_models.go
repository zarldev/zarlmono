package tui

import (
	"fmt"
	"strconv"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// activateEnum handles enter/←/→ on an enum row. Theme and codex-effort open a
// visible picker (so you choose from the full list with live preview instead
// of cycling blind); the rest cycle in place. dir is the cycle direction for
// the in-place case.
func (d *settingsDialog) activateEnum(dir int) action {
	r := d.curRow()
	switch r.key {
	case prefs.KeyTheme:
		return actionPush{d: newThemePickerFor(func(name string) { d.commit(prefs.KeyTheme, name) })}
	case prefs.KeyProvider:
		items := providerNames(d.s)
		sel := r.value
		if !r.isSet {
			sel = r.def
		}
		return actionPush{d: newListPicker("provider", items, sel, func(choice string) {
			d.commitModelSelection(d.s.DefaultModelSelection(choice))
			d.modelsLoading[choice] = true
			d.pendingFetch = choice
		})}
	case prefs.KeyCompactEngine:
		return actionPush{d: newListPicker("compaction engine", compactEngineOpts(), r.value, func(choice string) {
			d.commit(prefs.KeyCompactEngine, choice)
		})}
	case prefs.KeyCompactProvider,
		prefs.KeySpawnDefaultExploreProvider,
		prefs.KeySpawnDefaultVerifyProvider,
		prefs.KeySpawnDefaultImplementProvider:
		items := append([]string{compactActiveSentinel}, providerNames(d.s)...)
		sel := compactActiveSentinel
		if r.value != "" {
			sel = r.value
		}
		return actionPush{d: newListPicker("compaction provider", items, sel, func(choice string) {
			if choice == compactActiveSentinel {
				d.commit(r.key, "") // reuse the active provider
			} else {
				d.commit(r.key, choice)
			}
			if p := d.providerForRow(d.modelKeyForProvider(r.key)); p != "" {
				if _, ok := d.models[p]; !ok && !d.modelsLoading[p] {
					d.modelsLoading[p] = true
					d.pendingFetch = p
				}
			}
		})}
	case prefs.KeyJudgeProvider:
		items := append([]string{compactActiveSentinel}, providerNames(d.s)...)
		sel := compactActiveSentinel
		if r.value != "" {
			sel = r.value
		}
		return actionPush{d: newListPicker("judge provider", items, sel, func(choice string) {
			if choice == compactActiveSentinel {
				d.commit(prefs.KeyJudgeProvider, "") // reuse the active provider
			} else {
				d.commit(prefs.KeyJudgeProvider, choice)
			}
			// Queue a model fetch for the new judge provider so its model
			// picker is populated (a picker closure can't return a fetch).
			if p := d.judgeProvider(); p != "" {
				if _, ok := d.models[p]; !ok && !d.modelsLoading[p] {
					d.modelsLoading[p] = true
					d.pendingFetch = p
				}
			}
		})}
	case prefs.KeyCodexEffort:
		items := d.codexEffortItems()
		sel := codexEffortAuto
		if r.value != "" {
			sel = r.value
		}
		return actionPush{d: newListPicker("reasoning effort", items, sel, func(choice string) {
			val := choice
			if choice == codexEffortAuto {
				val = ""
			}
			d.commit(prefs.KeyCodexEffort, val)
		})}
	default:
		d.cycleEnum(dir)
		if r.key == prefs.KeyProvider {
			return d.onProviderCycled()
		}
	}
	return actionNone{}
}

const agentParentSentinel = "(parent/planner)"

func (d *settingsDialog) activateAgent() action {
	r := d.curRow()
	agents, _ := catalog.LoadAgents(wsRootOf(d.s))
	items := make([]string, 1, len(agents)+1)
	items[0] = agentParentSentinel
	for _, agent := range agents {
		items = append(items, agent.Name)
	}
	sel := r.value
	if sel == "" {
		sel = agentParentSentinel
	}
	return actionPush{d: newListPicker("default agent", items, sel, func(choice string) {
		if choice == agentParentSentinel {
			choice = ""
		}
		d.commit(r.key, choice)
	})}
}

// onProviderCycled runs after the provider enum changes: persist the provider
// and its model default as one transition, then request the new model list.
func (d *settingsDialog) onProviderCycled() action {
	provider := d.currentProvider()
	d.commitModelSelection(d.s.DefaultModelSelection(provider))
	return d.fetchForCurrentProvider()
}

// startEdit opens the inline editor on the current row, prefilled with its
// current value.
func (d *settingsDialog) startEdit() {
	r := d.curRow()
	d.editing = true
	d.editor = composer{}
	if r.kind != rowKey && r.isSet {
		d.editor.insert(r.value)
	}
}

// --- model list (per-provider, async-fetched) ---

// currentProvider is the effective provider the model picker fetches for:
// the provider row's set value, else its default.
func (d *settingsDialog) currentProvider() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyProvider {
				if r.isSet && r.value != "" {
					return r.value
				}
				return r.def
			}
		}
	}
	return ""
}

// compactProvider is the provider the compaction model picker fetches for:
// the compact_provider row's value, else the active provider (the engine
// reuses the active backend when no override is set).
func (d *settingsDialog) compactProvider() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyCompactProvider && r.isSet && r.value != "" {
				return r.value
			}
		}
	}
	return d.currentProvider()
}

// judgeProvider is the provider the judge model picker fetches for: the
// judge_provider row's value, else the active provider (the engine reuses the
// active backend when no override is set).
func (d *settingsDialog) judgeProvider() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyJudgeProvider && r.isSet && r.value != "" {
				return r.value
			}
		}
	}
	return d.currentProvider()
}

func (d *settingsDialog) modelKeyForProvider(key string) string {
	switch key {
	case prefs.KeySpawnDefaultExploreProvider:
		return prefs.KeySpawnDefaultExploreModel
	case prefs.KeySpawnDefaultVerifyProvider:
		return prefs.KeySpawnDefaultVerifyModel
	case prefs.KeySpawnDefaultImplementProvider:
		return prefs.KeySpawnDefaultImplementModel
	default:
		return prefs.KeyCompactModel
	}
}

func (d *settingsDialog) spawnProvider(key string) string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == key && r.isSet && r.value != "" {
				return r.value
			}
		}
	}
	return d.currentProvider()
}

func isSpawnDefaultModel(key string) bool {
	switch key {
	case prefs.KeySpawnDefaultExploreModel, prefs.KeySpawnDefaultVerifyModel, prefs.KeySpawnDefaultImplementModel:
		return true
	default:
		return false
	}
}

// providerForRow is the provider a model row's picker + hint resolve against:
// the compaction / judge model rows track their own provider rows, the main
// model row the active one.
func (d *settingsDialog) providerForRow(key string) string {
	switch key {
	case prefs.KeyCompactModel:
		return d.compactProvider()
	case prefs.KeyJudgeModel:
		return d.judgeProvider()
	case prefs.KeySpawnDefaultExploreModel:
		return d.spawnProvider(prefs.KeySpawnDefaultExploreProvider)
	case prefs.KeySpawnDefaultVerifyModel:
		return d.spawnProvider(prefs.KeySpawnDefaultVerifyProvider)
	case prefs.KeySpawnDefaultImplementModel:
		return d.spawnProvider(prefs.KeySpawnDefaultImplementProvider)
	}
	return d.currentProvider()
}

func (d *settingsDialog) activeModel() string {
	for _, c := range d.cats {
		for _, r := range c.rows {
			if r.key == prefs.KeyModel {
				if r.isSet && r.value != "" {
					return r.value
				}
				return r.def
			}
		}
	}
	return ""
}

func (d *settingsDialog) codexEffortItems() []string {
	items := []string{codexEffortAuto}
	if d.currentProvider() != backends.NameOpenAICodex.String() {
		return append(items, "low", "medium", "high", "xhigh", "max")
	}
	if variants := openaicodex.EffortVariants(d.activeModel()); len(variants) > 0 {
		return append(items, variants...)
	}
	return append(items, "low", "medium", "high", "xhigh", "max")
}

// takePendingFetch returns and clears any queued model-fetch provider.
func (d *settingsDialog) takePendingFetch() string {
	p := d.pendingFetch
	d.pendingFetch = ""
	return p
}

// fetchForCurrentProvider requests a model fetch for the active provider.
func (d *settingsDialog) fetchForCurrentProvider() action { return d.fetchFor(d.currentProvider()) }

// fetchFor requests a model fetch for provider unless it's already cached or
// in flight. Returns the push-to-root intent.
func (d *settingsDialog) fetchFor(p string) action {
	if p == "" {
		return actionNone{}
	}
	if _, ok := d.models[p]; ok || d.modelsLoading[p] {
		return actionNone{}
	}
	d.modelsLoading[p] = true
	return actionFetchModels{provider: p}
}

// onModelsLoaded records a completed fetch so the model picker can present
// the list.
func (d *settingsDialog) onModelsLoaded(provider string, models []string, err error) {
	if d.providers != nil && d.providers.onModelsLoaded(provider, models, err) {
		return
	}
	d.modelsLoading[provider] = false
	d.modelsLoaded[provider] = true
	d.modelsErr[provider] = err
	if err != nil && len(models) == 0 {
		d.setStatus("models: " + err.Error())
		return
	}
	d.models[provider] = models
	if provider == d.currentProvider() && len(models) > 0 {
		d.setStatus(fmt.Sprintf("%d models for %s", len(models), provider))
	}
}

// activateModel opens a picker over the fetched model list for the row's
// provider (the active one for the main model, the compaction provider for
// the compaction model), plus a custom-entry escape — and, for the compaction
// model, an "(active)" entry that clears the override. With nothing fetched it
// kicks a fetch and falls back to free-text entry.
func (d *settingsDialog) activateModel() action {
	key := d.curRow().key
	p := d.providerForRow(key)
	opts := d.models[p]
	if len(opts) == 0 {
		d.startEdit()
		return d.fetchFor(p) // populate for next time
	}
	items := make([]string, 0, len(opts)+2)
	if key == prefs.KeyCompactModel || key == prefs.KeyJudgeModel || isSpawnDefaultModel(key) {
		items = append(items, compactActiveSentinel)
	}
	items = append(items, modelCustomSentinel)
	items = append(items, opts...)
	meta := newModelInfoResolver(d.s)
	right := func(item string) string {
		if item == modelCustomSentinel || item == compactActiveSentinel {
			return ""
		}
		return meta.summary(p, item)
	}
	return actionPush{d: newListPickerWithRight("models · "+p, items, d.curRow().value, func(choice string) {
		switch choice {
		case modelCustomSentinel:
			d.startEdit()
		case compactActiveSentinel:
			d.commit(key, "") // reuse the active model
		default:
			d.commit(key, choice)
		}
	}, right)}
}

// modelHintFor is the trailing badge on a model row: the fetch state or the
// fetched count for the row's provider. Bare (no leading pad) so it joins into
// the badge column via joinBadges.
func (d *settingsDialog) modelHintFor(provider string) string {
	if d.modelsLoading[provider] {
		return palette.Warning.On("loading…")
	}
	if d.modelsErr[provider] != nil {
		return palette.Error.On("fetch failed")
	}
	if !d.modelsLoaded[provider] {
		return palette.Muted.On("not fetched")
	}
	if n := len(d.models[provider]); n > 0 {
		return palette.Subtle.On(strconv.Itoa(n) + " available")
	}
	return palette.Muted.On("no models · custom allowed")
}

func (d *settingsDialog) cycleEnum(dir int) {
	r := d.curRow()
	if r.kind != rowEnum || len(r.opts) == 0 {
		return
	}
	cur := r.value
	if !r.isSet {
		cur = r.def
	}
	idx := indexOf(r.opts, cur)
	if idx < 0 {
		idx = 0
	} else {
		idx = (idx + dir + len(r.opts)) % len(r.opts)
	}
	val := r.opts[idx]
	d.commit(r.key, val)
	if r.key == prefs.KeyTheme && val != "" {
		if t, ok := theme.ByName(val); ok {
			UseTheme(t) // live preview
		}
	}
}
