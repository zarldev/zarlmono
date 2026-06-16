package code_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestWriteAppend_CreatesFile(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	res := execTyped(t, tool, code.WriteAppendArgs{Path: "out.txt", Content: "hello"})
	if !res.Success {
		t.Fatalf("first append: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(ws.Root(), "out.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("file = %q, want hello", got)
	}
}

func TestWriteAppend_AppendsToExisting(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	// Three chunks → file should be the concatenation, in order.
	chunks := []string{"package main\n", "\nfunc Foo() {\n", "\treturn\n}\n"}
	for i, c := range chunks {
		res := execTyped(t, tool, code.WriteAppendArgs{Path: "main.go", Content: c})
		if !res.Success {
			t.Fatalf("chunk %d: %s", i, res.Error)
		}
	}
	got, err := os.ReadFile(filepath.Join(ws.Root(), "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(chunks, "")
	if string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestWriteAppend_CreatesParentDirs(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	res := execTyped(t, tool, code.WriteAppendArgs{Path: "deep/nested/file.txt", Content: "x"})
	if !res.Success {
		t.Fatalf("append: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), "deep/nested/file.txt")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestWriteAppend_RequiresPath(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	res := execTyped(t, tool, code.WriteAppendArgs{Content: "hello"})
	if res.Success {
		t.Error("expected failure when path missing")
	}
	if !strings.Contains(res.Error, "path required") {
		t.Errorf("expected 'path required' in error, got %q", res.Error)
	}
}

func TestWriteAppend_RejectsEscape(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	res := execTyped(t, tool, code.WriteAppendArgs{Path: "../escape.txt", Content: "x"})
	if res.Success {
		t.Error("expected failure for path escaping workspace")
	}
}

func TestWriteAppend_ReportsRunningSize(t *testing.T) {
	t.Parallel()
	ws := newTestWorkspace(t)
	tool := code.NewWriteAppendTool(ws)

	execTyped(t, tool, code.WriteAppendArgs{Path: "running.txt", Content: "AAA"})
	res := execTyped(t, tool, code.WriteAppendArgs{Path: "running.txt", Content: "BBB"})
	if !res.Success {
		t.Fatalf("second append: %s", res.Error)
	}
	// Status string should report current file size — useful for the
	// model to sanity-check it matches the body it intended to write.
	got, _ := res.Data.(string)
	if !strings.Contains(got, "size now 6") {
		t.Errorf("expected 'size now 6' in result, got %q", got)
	}
}

// --- helpers ---

func newTestWorkspace(t *testing.T) code.Workspace {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// execTyped invokes a tool with a typed args struct. JSON-round-trips
// the struct into a ToolParameters map so the call exercises the
// same DecodeArgs path the LLM runtime uses, while letting tests
// stay readable with field literals instead of map syntax.
func execTyped[T any](t *testing.T, tool tools.Tool, args T) *tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	var params tools.ToolParameters
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("unmarshal args into map: %v", err)
	}
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test-call",
		ToolName:  tool.Definition().Name,
		Arguments: params,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}
