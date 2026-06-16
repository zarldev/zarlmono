package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// colorDef is the JSON form of a theme colour: either a single hex
// string (shared light/dark) or {"light":..,"dark":..}. v2 uses dark.
type colorDef struct {
	Light string
	Dark  string
}

func (c *colorDef) UnmarshalJSON(data []byte) error {
	t := strings.TrimSpace(string(data))
	if t == "" || t == "null" {
		return nil
	}
	if t[0] == '{' {
		var o struct {
			Light string `json:"light"`
			Dark  string `json:"dark"`
		}
		if err := json.Unmarshal(data, &o); err != nil {
			return err
		}
		c.Light, c.Dark = o.Light, o.Dark
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("colour must be a hex string or {light,dark} object: %s", data)
	}
	c.Light, c.Dark = s, s
	return nil
}

// dark picks the dark variant, falling back to light.
func (c colorDef) dark() Color {
	if c.Dark != "" {
		return Color(c.Dark)
	}
	return Color(c.Light)
}

type themeFile struct {
	Name        string   `json:"name"`
	Bg          colorDef `json:"bg"`
	Fg          colorDef `json:"fg"`
	Subtle      colorDef `json:"subtle"`
	Muted       colorDef `json:"muted"`
	Primary     colorDef `json:"primary"`
	Secondary   colorDef `json:"secondary"`
	Success     colorDef `json:"success"`
	Warning     colorDef `json:"warning"`
	Error       colorDef `json:"error"`
	Info        colorDef `json:"info"`
	User        colorDef `json:"user"`
	Assistant   colorDef `json:"assistant"`
	Tool        colorDef `json:"tool"`
	System      colorDef `json:"system"`
	Border      colorDef `json:"border"`
	BorderFocus colorDef `json:"borderFocus"`
	Highlight   colorDef `json:"highlight"`
	Selection   colorDef `json:"selection"`
	PlanMode    colorDef `json:"planMode"`
}

// Decode parses a theme JSON (the v1-compatible schema), resolving each
// colour to its dark variant.
func Decode(data []byte) (Theme, error) {
	var f themeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return Theme{}, err
	}
	t := Theme{
		Name:        f.Name,
		Bg:          f.Bg.dark(),
		Fg:          f.Fg.dark(),
		Subtle:      f.Subtle.dark(),
		Muted:       f.Muted.dark(),
		Primary:     f.Primary.dark(),
		Secondary:   f.Secondary.dark(),
		Success:     f.Success.dark(),
		Warning:     f.Warning.dark(),
		Error:       f.Error.dark(),
		Info:        f.Info.dark(),
		User:        f.User.dark(),
		Assistant:   f.Assistant.dark(),
		Tool:        f.Tool.dark(),
		System:      f.System.dark(),
		Border:      f.Border.dark(),
		BorderFocus: f.BorderFocus.dark(),
		Highlight:   f.Highlight.dark(),
		Selection:   f.Selection.dark(),
		PlanMode:    f.PlanMode.dark(),
	}
	// Back-compat: themes predating the PlanMode slot inherit Warning.
	if t.PlanMode == "" {
		t.PlanMode = t.Warning
	}
	return t, nil
}

// LoadFile decodes a theme from a JSON file.
func LoadFile(path string) (Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, err
	}
	return Decode(data)
}

// LoadDir loads every *.json in dir (best-effort). A missing dir yields
// no themes and no error, matching the v1 convention.
func LoadDir(dir string) ([]Theme, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	var themes []Theme
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := LoadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		themes = append(themes, t)
	}
	return themes, errs
}
