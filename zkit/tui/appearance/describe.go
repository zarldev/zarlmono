package appearance

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// Describe reports effective selection and exact overrides in a common CLI
// format. A session palette is reported separately and is never persisted.
// It returns useful inherited information alongside any storage diagnostic.
func (s *Store) Describe(ctx context.Context, app, workspace, session string) ([]string, error) {
	choice, diagnostic := s.Resolve(ctx, app, workspace)
	if session != "" {
		palette, ok := theme.ByName(session)
		if !ok {
			return nil, fmt.Errorf("appearance: unknown session theme %q", session)
		}
		choice = Choice{Name: session, Scope: "session", Theme: palette}
	}
	lines := []string{
		fmt.Sprintf("effective: %s (source: %s)", choice.Name, choice.Scope),
		"workspace: " + workspace,
	}
	targets := []Target{Global()}
	if app != "" {
		targets = append(targets, App(app))
		if workspace != "" {
			targets = append(targets, Workspace(app, workspace))
		}
	}
	for _, target := range targets {
		name, set, err := s.Lookup(ctx, target)
		if err != nil {
			return lines, errors.Join(diagnostic, err)
		}
		if !set {
			name = "inherit"
		}
		lines = append(lines, target.Label()+": "+name)
	}
	return lines, diagnostic
}
