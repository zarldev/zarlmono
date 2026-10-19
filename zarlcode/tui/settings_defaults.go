package tui

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

func (d *settingsDialog) refreshTargetDefaults(ctx context.Context) {
	defaults := make(map[string]string)
	builtins := make(map[string]string)
	for _, key := range []string{
		prefs.KeyCompactProvider, prefs.KeyJudgeProvider,
		prefs.KeySpawnDefaultExploreProvider, prefs.KeySpawnDefaultVerifyProvider,
		prefs.KeySpawnDefaultImplementProvider,
	} {
		modelKey, def := prefs.ModelTargetDefault(key)
		builtins[key], builtins[modelKey] = def.Provider, def.Model
		target := d.s.ModelTarget(ctx, key)
		defaults[key], defaults[modelKey] = target.Name, target.Model
	}
	model := d.s.DefaultModelSelection(d.currentProvider()).Model
	if model == "" {
		model = "(provider default)"
	}
	defaults[prefs.KeyModel] = model
	for ci := range d.cats {
		for ri := range d.cats[ci].rows {
			r := d.cats[ci].rows[ri]
			if value, ok := defaults[r.key]; ok {
				if value == "" {
					value = compactActiveSentinel
				}
				r.def = value
				r.builtin = builtins[r.key]
				d.cats[ci].rows[ri] = r
			}
		}
	}
}

// commitTargetModel pins the provider shown by a secondary model picker along
// with its model, so a model-only edit cannot discard an unset built-in route.
func (d *settingsDialog) commitTargetModel(key, value string) {
	for _, providerKey := range []string{
		prefs.KeyCompactProvider, prefs.KeyJudgeProvider,
		prefs.KeySpawnDefaultExploreProvider, prefs.KeySpawnDefaultVerifyProvider,
		prefs.KeySpawnDefaultImplementProvider,
	} {
		modelKey, _ := prefs.ModelTargetDefault(providerKey)
		if modelKey != key {
			continue
		}
		target := d.s.ModelTarget(d.ctx, providerKey)
		provider := target.Name
		if provider == "" {
			provider = compactActiveSentinel
		}
		if err := d.s.Svc.ApplySettings(d.ctx, prefs.ScopeWorkspace,
			prefs.SettingChange{Key: providerKey, Value: provider},
			prefs.SettingChange{Key: key, Value: value},
		); err != nil {
			d.setStatus("error: " + err.Error())
		} else {
			d.setStatus(key + " → " + value + " (workspace)")
		}
		d.refresh(d.ctx)
		return
	}
}
