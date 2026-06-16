package code_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestWrite_CreatesNewFile(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	res := execTyped(t, tool, code.WriteArgs{Path: "hello.txt", Content: "hello world"})
	if !res.Success {
		t.Fatalf("write new file: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(ws.Root(), "hello.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("file = %q, want %q", got, "hello world")
	}
}

func TestWrite_CreatesParentDirs(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	res := execTyped(t, tool, code.WriteArgs{Path: "deep/nested/file.txt", Content: "x"})
	if !res.Success {
		t.Fatalf("write nested: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), "deep/nested/file.txt")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestWrite_RefusesExistingFile(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	// Pre-create the file at the workspace level so we don't depend on
	// a successful first write before testing the refusal.
	if err := os.WriteFile(filepath.Join(ws.Root(), "exists.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := execTyped(t, tool, code.WriteArgs{Path: "exists.go", Content: "package other\n"})
	if res.Success {
		t.Fatal("write over existing file: want refusal, got success")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Err.Kind = %v, want Validation", res.Err)
	}
	// File on disk must be untouched — that's the whole point of the invariant.
	got, err := os.ReadFile(filepath.Join(ws.Root(), "exists.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package main\n" {
		t.Errorf("file body = %q, want it untouched", got)
	}
}

func TestWrite_RefusalMessageGuidesToEdit(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)
	if err := os.WriteFile(filepath.Join(ws.Root(), "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := execTyped(t, tool, code.WriteArgs{Path: "a.txt", Content: "bye"})
	if res.Success {
		t.Fatal("want refusal")
	}
	for _, want := range []string{"already exists", "edit", "path", "old_string", "new_string", "read"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("refusal message missing %q: %q", want, res.Error)
		}
	}
}

func TestWrite_RefusesExistingEmptyFile(t *testing.T) {
	t.Parallel()
	// Strict invariant: an existing empty file is still existing. The
	// model has bash and edit; we don't carve an exception for size=0.
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)
	if err := os.WriteFile(filepath.Join(ws.Root(), "empty"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res := execTyped(t, tool, code.WriteArgs{Path: "empty", Content: "anything"})
	if res.Success {
		t.Fatal("write over existing empty file: want refusal")
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Err.Kind = %v, want Validation", res.Err)
	}
}

func TestWrite_SecondWriteToSamePathRefuses(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	if r := execTyped(t, tool, code.WriteArgs{Path: "x.txt", Content: "first"}); !r.Success {
		t.Fatalf("first write: %s", r.Error)
	}
	r := execTyped(t, tool, code.WriteArgs{Path: "x.txt", Content: "second"})
	if r.Success {
		t.Fatal("second write to same path: want refusal")
	}
	if r.Err == nil || r.Err.Kind != tools.Kinds.VALIDATION {
		t.Errorf("Err.Kind = %v, want Validation", r.Err)
	}
}

func TestWrite_RequiresPath(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	res := execTyped(t, tool, code.WriteArgs{Content: "hello"})
	if res.Success {
		t.Error("missing path: want failure")
	}
	if !strings.Contains(res.Error, "path required") {
		t.Errorf("error = %q, want it to mention 'path required'", res.Error)
	}
}

func TestWrite_RejectsEscape(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteTool(ws)

	res := execTyped(t, tool, code.WriteArgs{Path: "../escape.txt", Content: "x"})
	if res.Success {
		t.Error("path escape: want failure")
	}
}
