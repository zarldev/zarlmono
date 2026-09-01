package code_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestApplyPatchEOFMarkerPinsToTail(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, map[string]string{"f": "a {\n}\nb {\n}\n"})
	res := runPatch(t, tool, `*** Begin Patch
*** Update File: f
@@
 }
+tail
*** End of File
*** End Patch
`)
	if !res.Success {
		t.Fatalf("patch: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(root, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a {\n}\nb {\n}\ntail\n" {
		t.Fatalf("contents = %q, want EOF occurrence edited", got)
	}
}

func TestApplyPatchNonEOFMatchesFirst(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, map[string]string{"f": "a {\n}\nb {\n}\n"})
	res := runPatch(t, tool, `*** Begin Patch
*** Update File: f
@@
 }
+first
*** End Patch
`)
	if !res.Success {
		t.Fatalf("patch: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(root, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "a {\n}\nfirst\n") {
		t.Fatalf("contents = %q, want first occurrence edited", got)
	}
}

func TestApplyPatchEOFContextNotAtTailErrors(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, map[string]string{"f": "x\ny\nz\n"})
	res, err := tool.Execute(t.Context(), tools.ToolCall{ID: "test", Arguments: tools.ToolParameters{"patch": `*** Begin Patch
*** Update File: f
@@
 x
+new
*** End of File
*** End Patch
`}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Fatal("expected EOF context away from tail to be rejected")
	}
}
