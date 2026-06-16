package code_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Happy path: a clean name + content lands at .zarlcode/plans/<name>.md.
func TestSavePlan_WritesToPlansDir(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanTool(ws)

	body := "## Plan\n\n1. Step one.\n2. Step two.\n"
	res := execTyped(t, tool, code.SavePlanArgs{Name: "auth-rewrite", Content: body})
	if !res.Success {
		t.Fatalf("save_plan: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(ws.Root(), code.PlansDir, "auth-rewrite.md"))
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if string(got) != body {
		t.Errorf("plan body round-trip mismatch:\nwant %q\n got %q", body, got)
	}
}

// Empty name should fall back to a timestamp slug — the file should
// exist under plans/ with a name starting "plan-".
func TestSavePlan_DefaultsTimestampName(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanTool(ws)

	res := execTyped(t, tool, code.SavePlanArgs{Content: "minimal plan"})
	if !res.Success {
		t.Fatalf("save_plan: %s", res.Error)
	}
	entries, err := os.ReadDir(filepath.Join(ws.Root(), code.PlansDir))
	if err != nil {
		t.Fatalf("read plans dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("plans dir has %d entries, want 1", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "plan-") {
		t.Errorf("default name should start with 'plan-'; got %q", entries[0].Name())
	}
	if !strings.HasSuffix(entries[0].Name(), ".md") {
		t.Errorf("default name should end with '.md'; got %q", entries[0].Name())
	}
}

// Path-traversal attempts: a name with slashes, dots, or upward
// references must be rejected at the slug check, NEVER reach the
// filesystem. Verifies the path-locked invariant.
func TestSavePlan_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanTool(ws)

	cases := []string{
		"../escape",
		"sub/dir/plan",
		`..\windows\style`,
		"plan.md", // dots aren't in the allowed slug class
		"Capital", // uppercase rejected
		"with space",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := execTyped(t, tool, code.SavePlanArgs{Name: name, Content: "x"})
			if res.Success {
				t.Errorf("expected save_plan to reject name %q, but it succeeded", name)
			}
		})
	}
}

// Empty content is rejected — a save_plan call with no body has no
// purpose and is more likely a model bug than intent.
func TestSavePlan_RejectsEmptyContent(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanTool(ws)

	res := execTyped(t, tool, code.SavePlanArgs{Name: "valid", Content: ""})
	if res.Success {
		t.Error("save_plan should reject empty content")
	}
}

// Lazy mkdir: the plans directory shouldn't pre-exist; the first
// save creates it.
func TestSavePlan_CreatesPlansDirOnDemand(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanTool(ws)

	if _, err := os.Stat(filepath.Join(ws.Root(), code.PlansDir)); !os.IsNotExist(err) {
		t.Fatalf("plans dir shouldn't exist before save; stat err = %v", err)
	}
	res := execTyped(t, tool, code.SavePlanArgs{Content: "first plan in this workspace"})
	if !res.Success {
		t.Fatalf("save_plan: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), code.PlansDir)); err != nil {
		t.Fatalf("plans dir should exist after first save; got err: %v", err)
	}
}
