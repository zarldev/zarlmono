package appearance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// Picker holds an uncommitted selection; callers own applying previews and restoring
// the original palette on cancellation. No persistence occurs until Commit or Inherit.
type Picker struct {
	store             *Store
	app, workspace    string
	targets           []Target
	scopeIndex, index int
	themes            []theme.Theme
	effective         Choice
	diagnostic        error
	original          theme.Theme
}

// NewPicker starts at the most specific available target with the current palette selected.
func NewPicker(ctx context.Context, store *Store, app, workspace string, current theme.Theme) (*Picker, error) {
	if workspace != "" && app == "" {
		return nil, errors.New("appearance: workspace requires app")
	}
	effective, diagnostic := store.Resolve(ctx, app, workspace)
	targets := []Target{Global()}
	if app != "" {
		targets = append(targets, App(app))
	}
	if workspace != "" {
		targets = append(targets, Workspace(app, workspace))
	}
	palettes := append([]theme.Theme(nil), theme.Builtins()...)
	sort.Slice(palettes, func(i, j int) bool { return palettes[i].Name < palettes[j].Name })
	if len(palettes) == 0 {
		return nil, errors.New("appearance: empty theme catalogue")
	}
	p := &Picker{store: store, app: app, workspace: workspace, targets: targets, scopeIndex: len(targets) - 1, themes: palettes, effective: effective, diagnostic: diagnostic, original: current}
	for i, t := range palettes {
		if t.Name == effective.Name {
			p.index = i
			break
		}
	}
	for i, t := range palettes {
		if t.Name == current.Name {
			p.index = i
			break
		}
	}
	return p, nil
}

// Move wraps the preview selection through the sorted theme catalogue.
func (p *Picker) Move(delta int) { n := len(p.themes); p.index = ((p.index+delta)%n + n) % n }

// CycleScope advances through available global, app and workspace targets.
func (p *Picker) CycleScope() { p.scopeIndex = (p.scopeIndex + 1) % len(p.targets) }

// Preview returns the selected palette without changing persistence.
func (p *Picker) Preview() theme.Theme { return p.themes[p.index] }

// Original returns the exact opening palette, even if it has no catalogue name.
func (p *Picker) Original() theme.Theme { return p.original }

// Target returns the exact scope selected for the next save or inherit operation.
func (p *Picker) Target() Target { return p.targets[p.scopeIndex] }

// Scope returns the currently targeted override scope.
func (p *Picker) Scope() string { return p.targets[p.scopeIndex].Label() }

// Name returns the selected palette name.
func (p *Picker) Name() string { return p.Preview().Name }

// Lines renders a cell-clipped panel of at most height lines for a width-cell area.
func (p *Picker) Lines(width, height int) []string {
	return p.LinesWithStyle(width, height, chrome.Style{Theme: p.Preview()})
}

// LinesWithStyle renders the common picker with the caller's effective palette
// and ASCII/plain policy, keeping selected palettes visible without hardcoded colors.
func (p *Picker) LinesWithStyle(width, height int, style chrome.Style) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	header := []string{"theme appearance", fmt.Sprintf("effective: %s (%s)", p.effective.Name, p.effective.Scope), "target: " + p.Scope(), "selected: " + p.Name()}
	if p.diagnostic != nil {
		header = append(header, "notice: "+strings.Join(strings.Fields(p.diagnostic.Error()), " "))
	}
	footer := "up/down preview · tab scope · enter save · r inherit · esc cancel"
	if style.ASCII {
		footer = strings.ReplaceAll(footer, " · ", " / ")
	}
	rows := make([]string, 0, height)
	add := func(s string) {
		if len(rows) < height {
			rows = append(rows, style.Selection(style.Theme.Fg.On(s), width, false))
		}
	}
	for _, s := range header {
		add(s)
	}
	slots := height - len(rows) - 1
	if slots > 0 {
		start := p.index - slots/2
		if start < 0 {
			start = 0
		}
		if start > len(p.themes)-slots {
			start = max(0, len(p.themes)-slots)
		}
		for i := start; i < len(p.themes) && i < start+slots; i++ {
			marker := style.Marker(i == p.index, false)
			add(style.Selection(marker+p.themes[i].Name, width, i == p.index))
		}
	}
	if len(rows) < height {
		add(footer)
	} else {
		rows[len(rows)-1] = style.Selection(footer, width, false)
	}
	return rows
}

// Commit saves the selected palette at the target and resolves the resulting effective choice.
func (p *Picker) Commit(ctx context.Context) (Choice, error) {
	if err := p.store.Set(ctx, p.targets[p.scopeIndex], p.Name()); err != nil {
		return p.effective, err
	}
	choice, err := p.store.Resolve(ctx, p.app, p.workspace)
	if err == nil {
		p.effective = choice
		p.diagnostic = nil
	}
	return choice, err
}

// Inherit removes the targeted override and resolves the resulting effective choice.
func (p *Picker) Inherit(ctx context.Context) (Choice, error) {
	if err := p.store.Reset(ctx, p.targets[p.scopeIndex]); err != nil {
		return p.effective, err
	}
	choice, err := p.store.Resolve(ctx, p.app, p.workspace)
	if err == nil {
		p.effective = choice
		p.diagnostic = nil
	}
	return choice, err
}
