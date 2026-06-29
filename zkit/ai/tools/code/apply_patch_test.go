package code_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// applyPatchHarness builds a fresh workspace under t.TempDir() with
// the given pre-existing files and returns a tool ready to execute.
// fileSeeds is path → contents; paths are created relative to root.
func applyPatchHarness(t *testing.T, fileSeeds map[string]string) (*code.ApplyPatchTool, string) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range fileSeeds {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("seed %s: %v", rel, err)
		}
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return code.NewApplyPatchTool(ws), root
}

// runPatch invokes the tool with the given patch text and returns
// the ToolResult. Goes through the same Execute → DecodeArgs path
// the LLM runtime uses so failures land with the same shape.
func runPatch(t *testing.T, tool *code.ApplyPatchTool, patch string) *tools.ToolResult {
	t.Helper()
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test",
		Arguments: tools.ToolParameters{"patch": patch},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestApplyPatch_AddFile(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, nil)
	patch := `*** Begin Patch
*** Add File: greet.txt
+Hello
+world
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(root, "greet.txt"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "Hello\nworld\n" {
		t.Errorf("contents = %q, want %q", string(got), "Hello\nworld\n")
	}
}

func TestApplyPatch_AddFile_NestedDir(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, nil)
	patch := `*** Begin Patch
*** Add File: a/b/c.txt
+nested
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("expected success, got: %s", res.Error)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a/b/c.txt"))
	if string(got) != "nested\n" {
		t.Errorf("got %q", string(got))
	}
}

func TestApplyPatch_AddExistingRejects(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, map[string]string{"x.txt": "old"})
	patch := `*** Begin Patch
*** Add File: x.txt
+new
*** End Patch
`
	res := runPatch(t, tool, patch)
	if res.Success {
		t.Fatalf("expected failure on add-over-existing")
	}
	if !strings.Contains(res.Error, "already exists") {
		t.Errorf("err = %q", res.Error)
	}
}

func TestApplyPatch_UpdateFile_SingleHunk(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"package foo",
		"",
		"func Bar() int {",
		"\treturn 1",
		"}",
		"",
	}, "\n")
	tool, root := applyPatchHarness(t, map[string]string{"foo.go": src})
	patch := `*** Begin Patch
*** Update File: foo.go
@@
 func Bar() int {
-	return 1
+	return 42
 }
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}
	got, _ := os.ReadFile(filepath.Join(root, "foo.go"))
	want := strings.Replace(src, "\treturn 1", "\treturn 42", 1)
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", string(got), want)
	}
}

func TestApplyPatch_UpdateFile_MultiHunk(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"line one",
		"line two",
		"line three",
		"line four",
		"line five",
		"",
	}, "\n")
	tool, root := applyPatchHarness(t, map[string]string{"a.txt": src})
	patch := `*** Begin Patch
*** Update File: a.txt
@@
 line one
-line two
+line two replaced
 line three
@@
 line four
-line five
+line five replaced
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	want := strings.Join([]string{
		"line one",
		"line two replaced",
		"line three",
		"line four",
		"line five replaced",
		"",
	}, "\n")
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", string(got), want)
	}
}

func TestApplyPatch_UpdateFile_AnchorDisambiguates(t *testing.T) {
	t.Parallel()
	// Two functions with identical bodies — the @@ anchor picks the
	// right one without needing more context lines.
	src := strings.Join([]string{
		"class A:",
		"    def m(self):",
		"        return 1",
		"",
		"class B:",
		"    def m(self):",
		"        return 1",
		"",
	}, "\n")
	tool, root := applyPatchHarness(t, map[string]string{"x.py": src})
	patch := `*** Begin Patch
*** Update File: x.py
@@ class B
     def m(self):
-        return 1
+        return 2
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}
	got, _ := os.ReadFile(filepath.Join(root, "x.py"))
	// A's body should be untouched; B's body bumped to 2.
	wantA := "class A:\n    def m(self):\n        return 1\n"
	wantB := "class B:\n    def m(self):\n        return 2\n"
	if !strings.Contains(string(got), wantA) || !strings.Contains(string(got), wantB) {
		t.Errorf("got:\n%s", string(got))
	}
}

func TestApplyPatch_UpdateFile_AnchorMissing(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, map[string]string{"x.py": "class A:\n    pass\n"})
	patch := `*** Begin Patch
*** Update File: x.py
@@ class B
 class A:
-    pass
+    return None
*** End Patch
`
	res := runPatch(t, tool, patch)
	if res.Success {
		t.Fatalf("expected failure on missing anchor")
	}
	if !strings.Contains(res.Error, "anchor") {
		t.Errorf("err = %q (want anchor mention)", res.Error)
	}
}

func TestApplyPatch_DeleteFile(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, map[string]string{"goodbye.txt": "bye\n"})
	patch := `*** Begin Patch
*** Delete File: goodbye.txt
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("delete failed: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "goodbye.txt")); !os.IsNotExist(err) {
		t.Errorf("file still present: err=%v", err)
	}
}

func TestApplyPatch_DeleteMissingRejects(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, nil)
	patch := `*** Begin Patch
*** Delete File: nope.txt
*** End Patch
`
	res := runPatch(t, tool, patch)
	if res.Success {
		t.Fatalf("expected failure on delete-missing")
	}
}

func TestApplyPatch_UpdateRename(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, map[string]string{"old.go": "package x\nfunc Hi() {}\n"})
	patch := `*** Begin Patch
*** Update File: old.go
*** Move to: new.go
@@
 package x
-func Hi() {}
+func Hello() {}
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("rename+update failed: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "old.go")); !os.IsNotExist(err) {
		t.Errorf("old file should be gone: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "new.go"))
	if err != nil {
		t.Fatalf("read new: %v", err)
	}
	if !strings.Contains(string(got), "func Hello()") {
		t.Errorf("renamed file body wrong: %q", string(got))
	}
}

func TestApplyPatch_AtomicityOnPartialFailure(t *testing.T) {
	t.Parallel()
	// Two ops in one patch: first succeeds in isolation, second fails.
	// The whole patch must be a no-op.
	tool, root := applyPatchHarness(t, map[string]string{
		"a.txt": "alpha\n",
		"b.txt": "beta\n",
	})
	patch := `*** Begin Patch
*** Update File: a.txt
@@
-alpha
+ALPHA
*** Update File: b.txt
@@
-not-actually-here
+oops
*** End Patch
`
	res := runPatch(t, tool, patch)
	if res.Success {
		t.Fatalf("expected failure due to bad second hunk")
	}
	// a.txt MUST be unchanged because the planner aborts before commit.
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "alpha\n" {
		t.Errorf("a.txt was modified despite atomic-fail: %q", string(got))
	}
}

func TestApplyPatch_MultiFile(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, map[string]string{
		"keep.txt": "stay\n",
		"old.txt":  "remove me\n",
	})
	patch := `*** Begin Patch
*** Add File: new.txt
+fresh
*** Update File: keep.txt
@@
-stay
+stayed
*** Delete File: old.txt
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("multi-file patch failed: %s", res.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(got) != "fresh\n" {
		t.Errorf("new: %q", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(root, "keep.txt")); string(got) != "stayed\n" {
		t.Errorf("keep: %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Errorf("old.txt should be deleted")
	}
}

func TestApplyPatch_RejectsMissingEnvelope(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, nil)
	tests := []struct {
		name  string
		patch string
	}{
		{"no begin", `*** Add File: x.txt
+x
*** End Patch
`},
		{"no end", `*** Begin Patch
*** Add File: x.txt
+x
`},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := runPatch(t, tool, tt.patch)
			if res.Success {
				t.Errorf("expected failure for case %q", tt.name)
			}
		})
	}
}

func TestApplyPatch_RejectsUnknownLineKind(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, map[string]string{"x.txt": "hello\n"})
	patch := `*** Begin Patch
*** Update File: x.txt
@@
?nonsense line
*** End Patch
`
	res := runPatch(t, tool, patch)
	if res.Success {
		t.Errorf("expected parse failure")
	}
}

func TestApplyPatch_AcceptsFilelessHeaders(t *testing.T) {
	t.Parallel()
	// Models frequently drop the "File:" infix. All three op headers
	// must still parse in the short "*** Update path" form.
	tool, root := applyPatchHarness(t, map[string]string{
		"upd.txt": "before\n",
		"del.txt": "gone\n",
	})
	patch := `*** Begin Patch
*** Add created.txt
+made
*** Update upd.txt
@@
-before
+after
*** Delete del.txt
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("lenient headers rejected: %s", res.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "created.txt")); string(got) != "made\n" {
		t.Errorf("add via lenient header: got %q", string(got))
	}
	if got, _ := os.ReadFile(filepath.Join(root, "upd.txt")); string(got) != "after\n" {
		t.Errorf("update via lenient header: got %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(root, "del.txt")); !os.IsNotExist(err) {
		t.Errorf("delete via lenient header: file still present (err=%v)", err)
	}
}

func TestApplyPatch_MalformedHeaderGivesGrammarHint(t *testing.T) {
	t.Parallel()
	tool, _ := applyPatchHarness(t, map[string]string{"x.txt": "hi\n"})
	// Wrong verb after the directive marker: not a recognized header.
	patch := `*** Begin Patch
*** Modify File: x.txt
@@
-hi
+yo
*** End Patch
`
	res := runPatch(t, tool, patch)
	if res.Success {
		t.Fatal("expected parse failure on unknown directive")
	}
	if !strings.Contains(res.Error, "unrecognized patch directive") {
		t.Errorf("error should name the grammar, got: %q", res.Error)
	}
}

func TestApplyPatch_EndOfFileMarker(t *testing.T) {
	t.Parallel()
	tool, root := applyPatchHarness(t, map[string]string{"x.txt": "line1\nline2\n"})
	patch := `*** Begin Patch
*** Update File: x.txt
@@
 line2
+appended
*** End of File
*** End Patch
`
	res := runPatch(t, tool, patch)
	if !res.Success {
		t.Fatalf("update failed: %s", res.Error)
	}
	got, _ := os.ReadFile(filepath.Join(root, "x.txt"))
	if !strings.Contains(string(got), "appended") {
		t.Errorf("appended line missing: %q", string(got))
	}
}

func TestPatchPaths_EnumeratesEveryFileHeader(t *testing.T) {
	t.Parallel()
	patch := `*** Begin Patch
*** Add File: new.go
+package new
*** Update File: existing.go
*** Move to: renamed.go
@@
 keep
*** Delete File: gone.go
*** End Patch
`
	got := code.PatchPaths(patch)
	want := []string{"new.go", "existing.go", "renamed.go", "gone.go"}
	if !slices.Equal(got, want) {
		t.Errorf("PatchPaths = %v, want %v", got, want)
	}
}

func TestPatchPaths_DedupesRepeatedPath(t *testing.T) {
	t.Parallel()
	// Illegal patch (Add then Update of the same file) but cheap to defend against —
	// the diff recorder snapshot loop must not double-snapshot the same abs path.
	patch := `*** Begin Patch
*** Add File: foo.go
+x
*** Update File: foo.go
@@
 x
*** End Patch
`
	got := code.PatchPaths(patch)
	want := []string{"foo.go"}
	if !slices.Equal(got, want) {
		t.Errorf("PatchPaths = %v, want %v (must dedupe)", got, want)
	}
}

func TestPatchPaths_EmptyPatchReturnsNil(t *testing.T) {
	t.Parallel()
	if got := code.PatchPaths(""); got != nil {
		t.Errorf("PatchPaths(\"\") = %v, want nil", got)
	}
}

func TestPatchPaths_MalformedReturnsBestEffort(t *testing.T) {
	t.Parallel()
	// Missing Begin Patch envelope — parser would error, but the
	// path-extractor should still pick up the file headers it sees.
	// Defensive: even on a bad patch we want to know which files
	// the model intended to touch.
	patch := `*** Add File: stray.go
+content
`
	got := code.PatchPaths(patch)
	want := []string{"stray.go"}
	if !slices.Equal(got, want) {
		t.Errorf("PatchPaths = %v, want %v", got, want)
	}
}
