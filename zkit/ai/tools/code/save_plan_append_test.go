package code_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// Happy path: a clean scaffold-then-append sequence concatenates
// the chunks under .zarlcode/plans/<name>.md in order.
func TestSavePlanAppend_AppendsToScaffold(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	save := code.NewSavePlanTool(ws)
	appendTool := code.NewSavePlanAppendTool(ws)

	if r := execTyped(t, save, code.SavePlanArgs{Name: "huge", Content: "# Plan\n"}); !r.Success {
		t.Fatalf("scaffold: %s", r.Error)
	}
	chunks := []string{"\n## Step 1\n", "details\n", "\n## Step 2\n"}
	for i, c := range chunks {
		if r := execTyped(t, appendTool, code.SavePlanAppendArgs{Name: "huge", Content: c}); !r.Success {
			t.Fatalf("chunk %d: %s", i, r.Error)
		}
	}
	got, err := os.ReadFile(filepath.Join(ws.Root(), code.PlansDir, "huge.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Plan\n" + strings.Join(chunks, "")
	if string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// The append tool should create the plans dir on demand — letting a
// model that skips the scaffold step still produce a saved plan
// (the alternative is a Fatal that leaves the model wedged on the
// missing-parent path).
func TestSavePlanAppend_CreatesParentDir(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanAppendTool(ws)

	if _, err := os.Stat(filepath.Join(ws.Root(), code.PlansDir)); !os.IsNotExist(err) {
		t.Fatalf("plans dir shouldn't exist yet; stat err = %v", err)
	}
	res := execTyped(t, tool, code.SavePlanAppendArgs{Name: "fresh", Content: "hi"})
	if !res.Success {
		t.Fatalf("append: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), code.PlansDir, "fresh.md")); err != nil {
		t.Errorf("expected file under plans dir: %v", err)
	}
}

// Path traversal at the slug level — same shape as save_plan's
// rejection list. save_plan_append shares the regex so it must
// produce the same refusals.
func TestSavePlanAppend_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanAppendTool(ws)

	cases := []string{
		"../escape",
		"sub/dir/plan",
		`..\windows\style`,
		"plan.md",
		"Capital",
		"with space",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := execTyped(t, tool, code.SavePlanAppendArgs{Name: name, Content: "x"})
			if res.Success {
				t.Errorf("expected refusal for name %q, got success", name)
			}
		})
	}
}

func TestSavePlanAppend_RequiresName(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanAppendTool(ws)

	res := execTyped(t, tool, code.SavePlanAppendArgs{Content: "x"})
	if res.Success {
		t.Error("expected failure when name missing")
	}
	if !strings.Contains(res.Error, "name required") {
		t.Errorf("expected 'name required' in error, got %q", res.Error)
	}
}

func TestSavePlanAppend_RequiresContent(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanAppendTool(ws)

	res := execTyped(t, tool, code.SavePlanAppendArgs{Name: "valid"})
	if res.Success {
		t.Error("expected failure when content missing")
	}
}

// The save_plan oversize-error should now name save_plan_append as
// the recovery path so the model can recover on the next turn
// instead of looping on the same JSON-parse error.
func TestSavePlan_OversizeErrorPointsToAppend(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewSavePlanTool(ws)

	// 300KB exceeds the 256KB default cap.
	big := strings.Repeat("x", 300*1024)
	res := execTyped(t, tool, code.SavePlanArgs{Name: "oversize", Content: big})
	if res.Success {
		t.Fatalf("expected oversize content to be rejected, got success")
	}
	if !strings.Contains(res.Error, "save_plan_append") {
		t.Errorf("oversize error should name save_plan_append as the recovery path; got %q", res.Error)
	}
}
