package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsModelVerbosityPicker(t *testing.T) {
	for index, choice := range []string{"(unset)", "low", "medium", "high"} {
		t.Run(choice, func(t *testing.T) {
			s := newTestSettings(t)
			ctx := t.Context()
			if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyTextVerbosity, "high"); err != nil {
				t.Fatal(err)
			}
			if err := s.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyCodexEffort, "high"); err != nil {
				t.Fatal(err)
			}
			ui := tui.New()
			ui.SetWorkspace(t.TempDir(), "gpt-6.1-sol")
			ui.SetSettings(s)
			ui.Update(tea.WindowSizeMsg{Width: 180, Height: 60})
			ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			view := ansi.Strip(ui.View().Content)
			reasoning, verbosity := strings.Index(view, "reasoning effort"), strings.Index(view, "text verbosity")
			if reasoning < 0 || verbosity <= reasoning || strings.Count(view, "text verbosity") != 1 {
				t.Fatalf("verbosity not discoverable beside reasoning in model settings:\n%s", view)
			}

			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			for range 4 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			help := ansi.Strip(ui.View().Content)
			for _, want := range []string{"independently of reasoning effort", "not a token limit", "applies next turn"} {
				if !strings.Contains(help, want) {
					t.Fatalf("verbosity help missing %q:\n%s", want, help)
				}
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			view = ansi.Strip(ui.View().Content)
			_, picker, ok := strings.Cut(view, "│ text verbosity ·")
			if !ok {
				t.Fatalf("verbosity picker not opened:\n%s", view)
			}
			for _, option := range []string{"(unset)", "low", "medium", "high"} {
				if !strings.Contains(picker, option) {
					t.Fatalf("picker missing %q:\n%s", option, picker)
				}
			}
			// Inheriting high must not write anything merely by opening/cancelling.
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if _, err := s.Svc.GetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTextVerbosity); !errors.Is(err, prefs.ErrNotFound) {
				t.Fatalf("cancel changed workspace verbosity: %v", err)
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			for range 3 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			}
			for range index {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			saved, err := s.Svc.GetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyTextVerbosity)
			if err != nil || saved.Value != choice {
				t.Fatalf("workspace verbosity=%q, %v; want %q", saved.Value, err, choice)
			}
			global, err := s.Svc.GetSetting(ctx, prefs.ScopeGlobal, prefs.KeyTextVerbosity)
			if err != nil || global.Value != "high" {
				t.Fatalf("changed global verbosity: %q, %v", global.Value, err)
			}
			target := engine.ProviderSpec{Name: "openai-codex", Model: "gpt-6.1-sol"}
			want := choice
			if choice == "(unset)" {
				want = ""
			}
			if got := s.TextVerbosity(ctx, target); got != want {
				t.Fatalf("effective request verbosity=%q, want %q", got, want)
			}
			if got := s.CodexEffort(ctx, target); got != "high" {
				t.Fatalf("verbosity changed reasoning effort: %q", got)
			}

			ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			for range 6 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			if view := ansi.Strip(ui.View().Content); strings.Contains(view, "text verbosity") {
				t.Fatalf("duplicate verbosity control remains under tools:\n%s", view)
			}
		})
	}
}
