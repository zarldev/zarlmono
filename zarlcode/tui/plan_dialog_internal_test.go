package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestPlanLines_Empty(t *testing.T) {
	got := strings.Join(planLines(code.Plan{}, planWrapWidth), "\n")
	if !strings.Contains(got, "no structured plan yet") {
		t.Errorf("empty plan should show the placeholder hint, got %q", got)
	}
}

func TestPlanLines_StatusGlyphsAndCounts(t *testing.T) {
	p := code.Plan{
		Steps: []code.PlanStep{
			{Text: "read the failing test", Status: code.StepStatuses.COMPLETED},
			{Text: "patch the handler", Status: code.StepStatuses.INPROGRESS},
			{Text: "re-run the suite", Status: code.StepStatuses.PENDING},
		},
		Explanation: "split the patch step after reading",
	}
	got := strings.Join(planLines(p, planWrapWidth), "\n")

	for _, want := range []string{"✓ read the failing test", "▶ patch the handler", "☐ re-run the suite"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing step row %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "3 steps · 1 done · 1 in progress · 1 pending") {
		t.Errorf("missing/incorrect counts line in:\n%s", got)
	}
	if !strings.Contains(got, "latest update: split the patch step after reading") {
		t.Errorf("missing explanation in:\n%s", got)
	}
}

func TestPlanNotice_ProgressAndCleared(t *testing.T) {
	p := code.Plan{Steps: []code.PlanStep{
		{Text: "a", Status: code.StepStatuses.COMPLETED},
		{Text: "b", Status: code.StepStatuses.PENDING},
	}}
	if got := planNotice(p); !strings.Contains(got, "1/2 done") {
		t.Errorf("planNotice progress: got %q", got)
	}
	if got := planNotice(code.Plan{}); !strings.Contains(got, "cleared") {
		t.Errorf("planNotice empty: got %q", got)
	}
}

func TestPlanDialogKeyBehavior(t *testing.T) {
	d := newPlanDialog(&code.Plan{}, "/tmp/test")
	if _, ok := d.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}).(actionNone); !ok {
		t.Fatal("enter should not close the plan pane")
	}
	for _, key := range []tea.KeyPressMsg{
		{Mod: tea.ModCtrl, Code: 'p'},
		{Code: tea.KeyEsc},
		{Text: "q", Code: 'q'},
	} {
		if _, ok := d.handleKey(key).(actionClose); !ok {
			t.Fatalf("%q should close the plan pane", key.String())
		}
	}
}

func TestPlanDialogSavedViewChrome(t *testing.T) {
	dir := t.TempDir()
	plansDir := filepath.Join(dir, code.PlansDir)
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir plans dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plansDir, "alpha.md"), []byte("# Alpha\n\nHello"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	d := newPlanDialog(&code.Plan{}, dir)
	d.view = planViewSaved
	d.tryPreview()
	buf := uv.NewScreenBuffer(120, 30)
	d.drawSaved(buf, uv.Rect(0, 0, 120, 30))
	out := ansi.Strip(buf.String())
	for _, want := range []string{"plan", "saved plans · newest first", "alpha", "source: saved plan markdown"} {
		if !strings.Contains(out, want) {
			t.Fatalf("saved plan view missing %q:\n%s", want, out)
		}
	}
}

func TestPlanDialogTryPreview_ReloadsWhenFileChangesAtSamePath(t *testing.T) {
	dir := t.TempDir()
	plansDir := filepath.Join(dir, code.PlansDir)
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir plans dir: %v", err)
	}
	path := filepath.Join(plansDir, "alpha.md")
	if err := os.WriteFile(path, []byte("# Alpha\n\nfirst"), 0o644); err != nil {
		t.Fatalf("write initial plan: %v", err)
	}
	d := newPlanDialog(&code.Plan{}, dir)
	d.view = planViewSaved
	d.tryPreview()
	if !strings.Contains(d.previewContent, "first") {
		t.Fatalf("initial preview = %q, want first version", d.previewContent)
	}
	if err := os.WriteFile(path, []byte("# Alpha\n\nsecond"), 0o644); err != nil {
		t.Fatalf("rewrite plan: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat rewritten plan: %v", err)
	}
	d.entries[0].modTime = info.ModTime()
	d.tryPreview()
	if !strings.Contains(d.previewContent, "second") {
		t.Fatalf("stale preview after rewrite = %q, want reloaded content", d.previewContent)
	}
}
