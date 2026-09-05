package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestCatalogDetailWindowsAroundSelectedEntry(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		body := fmt.Sprintf("---\nname: agent-%02d\ndescription: item %02d\n---\nbody %02d\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("agent-%02d.md", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := tui.New()
	m.SetWorkspace(root, "")
	m.SetSettings(newTestSettingsAt(t, root))
	step(t, m, window(80, 14))
	step(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 's'})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // providers
	step(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // catalog
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "agent-39") || strings.Contains(out, "agent-00") {
		t.Fatalf("catalog was not windowed around selection:\n%s", out)
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if out = ansi.Strip(m.View().Content); !strings.Contains(out, "body 39") {
		t.Fatalf("expanded body missing:\n%s", out)
	}
}
