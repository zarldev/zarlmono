package tui

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

const appearanceApp = "zarlcode"

type themeSettings interface {
	GetSetting(context.Context, prefs.Scope, string) (prefs.SettingValue, error)
	DeleteSetting(context.Context, prefs.Scope, string) error
}

// themeChoice resolves shared appearance overrides alongside explicit legacy
// zarlcode preferences, without migrating or writing either store on startup.
func themeChoice(ctx context.Context, store *appearance.Store, svc themeSettings, workspace string) (appearance.Choice, error) {
	base, diagnostic := store.Resolve(ctx, appearanceApp, workspace)
	legacy := func(scope prefs.Scope, label string) (appearance.Choice, bool, error) {
		if svc == nil {
			return appearance.Choice{}, false, nil
		}
		value, e := svc.GetSetting(ctx, scope, prefs.KeyTheme)
		if errors.Is(e, prefs.ErrNotFound) {
			return appearance.Choice{}, false, nil
		}
		if e != nil {
			return appearance.Choice{}, false, fmt.Errorf("legacy theme %s: %w", label, e)
		}
		if value.Value == "" {
			return appearance.Choice{}, false, nil
		}
		t, ok := theme.ByName(value.Value)
		if !ok {
			return appearance.Choice{}, false, fmt.Errorf("legacy theme %s: unknown theme %q", label, value.Value)
		}
		return appearance.Choice{Name: value.Value, Scope: label, Theme: t}, true, nil
	}
	for _, item := range []struct {
		target      appearance.Target
		legacy      prefs.Scope
		label       string
		checkLegacy bool
	}{
		{appearance.Workspace(appearanceApp, workspace), prefs.ScopeWorkspace, "workspace", workspace != ""},
		{appearance.App(appearanceApp), prefs.ScopeGlobal, "app", true},
		{appearance.Global(), prefs.ScopeGlobal, "global", false},
	} {
		if item.target.Label() == base.Scope {
			return base, diagnostic
		}
		if item.checkLegacy {
			choice, set, e := legacy(item.legacy, item.label)
			if e != nil {
				diagnostic = errors.Join(diagnostic, e)
				continue
			}
			if set {
				return choice, diagnostic
			}
		}
	}
	return base, diagnostic
}

func appearanceStore() (*appearance.Store, error) {
	path, err := appearance.DefaultPath()
	if err != nil {
		return nil, err
	}
	return appearance.New(path), nil
}

// resetThemeScope removes the matching legacy preference as well as the shared
// override, so inheritance cannot resurrect an old selection.
func resetThemeScope(ctx context.Context, store *appearance.Store, svc themeSettings, target appearance.Target) error {
	// Do not delete a usable legacy selection when the family store cannot be
	// reset. If legacy deletion fails afterwards, it remains effective and the
	// explicit error lets the user retry without losing that selection.
	if err := store.Reset(ctx, target); err != nil {
		return err
	}
	if svc != nil {
		switch target.Label() {
		case "workspace":
			return svc.DeleteSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTheme)
		case "app":
			return svc.DeleteSetting(ctx, prefs.ScopeGlobal, prefs.KeyTheme)
		}
	}
	return nil
}
