package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsSpeedPicker(t *testing.T) {
	for _, tc := range []struct {
		provider, model string
		want            []string
		absent          []string
	}{
		{"openai", "gpt-6-astra", []string{"standard", "fast", "ultrafast"}, nil},
		{"openai-codex", "gpt-6.1-sol", []string{"standard", "fast"}, []string{"ultrafast"}},
		{"anthropic", "claude", nil, []string{"standard", "fast", "ultrafast"}},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			s := newTestSettings(t)
			if err := s.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, prefs.ModelSelection{Provider: tc.provider, Model: tc.model}); err != nil {
				t.Fatal(err)
			}
			ui := tui.New()
			ui.SetWorkspace(t.TempDir(), "")
			ui.SetSettings(s)
			ui.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
			ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			for range 6 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			help := ansi.Strip(ui.View().Content)
			if !strings.Contains(help, "estimates exclude tier premiums") {
				t.Fatalf("missing speed cost warning:\n%s", help)
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			view := ansi.Strip(ui.View().Content)
			_, picker, ok := strings.Cut(view, "│ speed ·")
			if !ok {
				t.Fatalf("speed picker not opened:\n%s", view)
			}
			for _, want := range append([]string{"(auto)"}, tc.want...) {
				if !strings.Contains(picker, want) {
					t.Errorf("speed picker missing %q:\n%s", want, view)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(picker, absent) {
					t.Errorf("speed picker offers unsupported %q:\n%s", absent, view)
				}
			}
			want := "(auto)"
			if len(tc.want) > 0 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				want = "fast"
			}
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			got, err := s.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyServiceTier)
			if err != nil || got.Value != want {
				t.Fatalf("saved speed = %q, %v; want %q", got.Value, err, want)
			}
		})
	}
}
