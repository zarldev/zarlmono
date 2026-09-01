package tui_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestPlanDialogRendersStatusCountsAndCloses(t *testing.T) {
	steps := []code.PlanStep{{Text: "read the failing test", Status: code.StepStatuses.COMPLETED}, {Text: "patch the handler", Status: code.StepStatuses.INPROGRESS}, {Text: "re-run the suite", Status: code.StepStatuses.PENDING}}
	for i := 4; i <= 18; i++ {
		steps = append(steps, code.PlanStep{Text: "step " + strconv.Itoa(i), Status: code.StepStatuses.PENDING})
	}
	m := tui.New()
	m.SetPlan(code.Plan{Steps: steps, Explanation: "split the patch step after reading"})
	stepUI(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'p'})
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[planning pane]", "[ live ]", "18 steps", "✓ read the failing test", "▶ patch the handler", "1 done", "tab saved plans", "updates live"} {
		if !strings.Contains(out, want) {
			t.Errorf("live plan missing %q:\n%s", want, out)
		}
	}
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if out = ansi.Strip(m.View().Content); !strings.Contains(out, "step 18") {
		t.Fatalf("end did not reveal final step:\n%s", out)
	}
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if strings.Contains(ansi.Strip(m.View().Content), "[planning pane]") {
		t.Fatal("escape did not close plan pane")
	}
}

func TestPlanDialogEmptyAndSavedPlans(t *testing.T) {
	dir := t.TempDir()
	plans := filepath.Join(dir, code.PlansDir)
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plans, "alpha.md"), []byte("# Alpha\n\nfirst"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := tui.New()
	m.SetWorkspace(dir, "")
	m.SetPlan(code.Plan{})
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'p'})
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "no structured plan yet") {
		t.Fatalf("empty plan missing hint:\n%s", out)
	}
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[ saved ]", "saved plans · newest first", "alpha", "source: saved plan markdown", "esc close"} {
		if !strings.Contains(out, want) {
			t.Errorf("saved plan missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(out, "\n")
	if strings.HasPrefix(lines[0], "┌") || strings.HasPrefix(lines[len(lines)-1], "└") {
		t.Fatalf("saved plans used outer box:\n%s", out)
	}
}
