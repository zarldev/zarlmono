package code_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestWriteTool_EmitsFileCreateEffect(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	res := execTyped(t, tool, code.WriteArgs{Path: "new.go", Content: "package p\n"})
	assertOneFileEffect(t, res, tools.FileCreate, "new.go")
	if got := res.FileEffects()[0].BytesAfter; got != int64(len("package p\n")) {
		t.Fatalf("BytesAfter = %d, want %d", got, len("package p\n"))
	}
}

func TestWriteAppendTool_EmitsFileAppendEffect(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	res := execTyped(t, tool, code.WriteAppendArgs{Path: "log.txt", Content: "hello"})
	assertOneFileEffect(t, res, tools.FileAppend, "log.txt")
	if got := res.FileEffects()[0].BytesAfter; got != int64(len("hello")) {
		t.Fatalf("BytesAfter = %d, want %d", got, len("hello"))
	}
}

func TestEditTool_EmitsFileModifyEffect(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws.Root(), "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tool := code.NewEditTool(ws)

	res := execTyped(t, tool, code.EditArgs{Path: "main.go", OldString: "package main", NewString: "package demo"})
	assertOneFileEffect(t, res, tools.FileModify, "main.go")
	if got := res.FileEffects()[0].BytesAfter; got != int64(len("package demo\n")) {
		t.Fatalf("BytesAfter = %d, want %d", got, len("package demo\n"))
	}
}

func TestApplyPatchTool_EmitsFileEffects(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws.Root(), "old.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed old.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), "delete.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed delete.go: %v", err)
	}
	tool := code.NewApplyPatchTool(ws)

	patch := `*** Begin Patch
*** Add File: new.go
+package main
*** Update File: old.go
@@
-package main
+package demo
*** Delete File: delete.go
*** End Patch`

	res := execTyped(t, tool, code.ApplyPatchArgs{Patch: patch})
	if !res.Success {
		t.Fatalf("apply_patch: %s", res.Error)
	}
	got := map[string]tools.FileOp{}
	for _, e := range res.FileEffects() {
		got[e.Path] = e.Op
	}
	want := map[string]tools.FileOp{
		"new.go":    tools.FileCreate,
		"old.go":    tools.FileModify,
		"delete.go": tools.FileDelete,
	}
	for path, op := range want {
		if got[path] != op {
			t.Fatalf("effect for %s = %q, want %q (all effects: %+v)", path, got[path], op, res.FileEffects())
		}
	}
}

func assertOneFileEffect(t *testing.T, res *tools.ToolResult, op tools.FileOp, path string) {
	t.Helper()
	if !res.Success {
		t.Fatalf("result failed: %s", res.Error)
	}
	files := res.FileEffects()
	if len(files) != 1 {
		t.Fatalf("FileEffects len = %d, want 1 (%+v)", len(files), files)
	}
	if files[0].Op != op || files[0].Path != path {
		t.Fatalf("FileEffects[0] = %+v, want %s %s", files[0], op, path)
	}
}
