package theme

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed themes/*.json
var builtinFS embed.FS

var (
	builtins    []Theme
	byName      = map[string]Theme{}
	darkDefault Theme
	errLoad     error
)

func init() {
	themes, dark, err := loadBuiltins()
	errLoad = err
	builtins = themes
	darkDefault = dark
	for _, t := range themes {
		byName[t.Name] = t
	}
}

// LoadBuiltins reads and decodes every embedded theme, returning them in
// directory order. Unlike the package-level accessors it surfaces failures:
// the returned error aggregates (via errors.Join) every per-file read or
// decode failure so a corrupt embedded theme is diagnosable instead of
// silently dropped. The themes that DID decode are still returned alongside
// any error, so a caller can use the good subset and log the rest.
func LoadBuiltins() ([]Theme, error) {
	themes, _, err := loadBuiltins()
	return themes, err
}

// LoadError reports any failure encountered loading the embedded themes at
// init time, nil on a clean load. The TUI surfaces it once at startup so a
// corrupt embedded theme leaves a diagnostic rather than a silent palette
// fallback.
func LoadError() error { return errLoad }

// loadBuiltins does the embed walk shared by init and LoadBuiltins. It
// returns the decoded themes in directory order, the theme flagged
// default:dark (or the first theme as a fallback), and a joined error over
// every per-file failure.
func loadBuiltins() ([]Theme, Theme, error) {
	entries, err := builtinFS.ReadDir("themes")
	if err != nil {
		return nil, Theme{}, fmt.Errorf("theme: read embedded themes dir: %w", err)
	}
	var (
		themes []Theme
		dark   Theme
		errs   []error
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, rerr := builtinFS.ReadFile("themes/" + e.Name())
		if rerr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", e.Name(), rerr))
			continue
		}
		t, derr := Decode(data)
		if derr != nil {
			errs = append(errs, fmt.Errorf("decode %s: %w", e.Name(), derr))
			continue
		}
		// The default flag is optional; Decode already validated the JSON,
		// so this re-unmarshal of the one field can't fail in practice.
		var meta struct {
			Default string `json:"default"`
		}
		_ = json.Unmarshal(data, &meta)
		if strings.EqualFold(meta.Default, "dark") {
			dark = t
		}
		themes = append(themes, t)
	}
	// Guarantee a usable default even if no theme declares default:dark.
	if dark.Name == "" && len(themes) > 0 {
		dark = themes[0]
	}
	return themes, dark, errors.Join(errs...)
}

// Builtins returns every embedded theme.
func Builtins() []Theme { return builtins }

// DarkDefault returns the builtin theme marked default:dark (or the first
// builtin as a fallback).
func DarkDefault() Theme { return darkDefault }

// ByName returns the builtin theme with the given name.
func ByName(name string) (Theme, bool) {
	t, ok := byName[name]
	return t, ok
}
