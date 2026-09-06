package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestSettingsUsesIndividualPaneHeaders(t *testing.T) {
	for _, size := range []struct{ width, height int }{{120, 32}, {48, 12}, {24, 5}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			ui := newSettingsLayoutUI(t, size.width, size.height)
			for i, category := range []string{"model", "providers", "catalog", "context", "limits", "safety", "tools", "mcp", "appearance", "interface"} {
				if i > 0 {
					ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				}
				view := ansi.Strip(ui.View().Content)
				lines := strings.Split(view, "\n")
				navHead, detailHead, ok := strings.Cut(lines[0], "┐┌")
				if !ok || !strings.HasPrefix(navHead, "┌") || !strings.Contains(navHead, "[") || !strings.Contains(detailHead, "["+category+"]") {
					t.Fatalf("expected individual bracketed pane headers, not a floating title:\n%s", view)
				}
				if size.width >= 48 && !strings.Contains(navHead, "[settings]") {
					t.Fatalf("settings title missing from navigation frame: %q", navHead)
				}
				if ansi.StringWidth(lines[0]) != size.width || !strings.HasSuffix(lines[0], "┐") {
					t.Fatalf("headers should span their pane borders: %q", lines[0])
				}
				if i == 0 {
					nav, row, _ := strings.Cut(lines[1], "││")
					if !strings.HasPrefix(nav, "│▸ ") || !strings.Contains(row, "provider") {
						t.Fatalf("category and settings rows should begin immediately below their headers: %q", lines[1])
					}
				}
				if !strings.Contains(lines[size.height-2], "┘└") || !strings.Contains(lines[size.height-1], "↑↓ category") {
					t.Fatalf("category %q lost pane borders or footer:\n%s", category, view)
				}
			}
		})
	}
}

func TestSettingsSectionsUseJoinedHeaderBars(t *testing.T) {
	ui := newSettingsLayoutUI(t, 120, 60)
	for i, sections := range [][]string{
		{"provider"},
		{"help"},
		{"help"},
		{"headroom", "compaction", "reserve tokens"},
		{"sub-agents", "enable sub-agents"},
		{"guardrails", "shell", "verification", "credentials", "plan first"},
		{"surface", "services", "processes", "web tools"},
		{"servers", "help"},
		{"help"},
		{"diagnostics", "confirm quit"},
	} {
		if i > 0 {
			ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		view := ansi.Strip(ui.View().Content)
		lines := strings.Split(view, "\n")
		for _, section := range sections {
			found := false
			for _, line := range lines {
				if strings.Contains(line, "├─["+section+"]") {
					found = true
					if !strings.HasSuffix(line, "┤") || ansi.StringWidth(line) != 120 {
						t.Fatalf("section %q should join both pane borders: %q", section, line)
					}
					break
				}
			}
			if !found {
				t.Fatalf("missing section header bar %q:\n%s", section, view)
			}
		}
		if i == 0 || i == 3 {
			t.Logf("rendered pane headers and sections:\n%s", strings.Join(lines[:16], "\n"))
		}
	}
}

func TestSettingsHeadingStaysAboveScrollingRowsAndEditor(t *testing.T) {
	ui := newSettingsLayoutUI(t, 80, 6)
	for range 4 {
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})  // max iterations
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // inline editor

	view := ansi.Strip(ui.View().Content)
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "[limits]") {
		t.Fatalf("scrolling or editing overwrote pane header:\n%s", view)
	}
	body := strings.Join(lines[1:4], "\n")
	if !strings.Contains(body, "max iterations") || !strings.Contains(body, "▏") {
		t.Fatalf("selected editor should remain between header and footer:\n%s", view)
	}
	if !strings.Contains(lines[5], "save") || !strings.Contains(lines[5], "cancel") {
		t.Fatalf("editor footer overwritten: %q", lines[5])
	}
}

func newSettingsLayoutUI(t *testing.T, width, height int) *tui.UI {
	t.Helper()
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "")
	ui.SetSettings(&engine.Settings{})
	ui.Update(tea.WindowSizeMsg{Width: width, Height: height})
	ui.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	return ui
}
