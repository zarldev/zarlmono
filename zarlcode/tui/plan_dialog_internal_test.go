package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
