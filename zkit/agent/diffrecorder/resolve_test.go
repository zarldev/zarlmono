package diffrecorder_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRecorder_RelativeEscapeIsNotSnapshotted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"write": func(_ context.Context, _ tools.ToolCall) (*tools.ToolResult, error) {
			called = true
			if err := os.WriteFile(outside, []byte("after\n"), 0o644); err != nil {
				return nil, err
			}
			return &tools.ToolResult{Success: true}, nil
		},
	}}
	rec := diffrecorder.New(src, root, diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": "../outside.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("recorder did not pass the call to the wrapped source")
	}
	if got := sink.entries(); len(got) != 0 {
		t.Fatalf("workspace escape produced diff events: %+v", got)
	}
}

func TestRecorder_UncleanRootStillCapturesContainedPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"write": func(_ context.Context, _ tools.ToolCall) (*tools.ToolResult, error) {
			return &tools.ToolResult{Success: true}, os.WriteFile(path, []byte("after\n"), 0o644)
		},
	}}
	rec := diffrecorder.New(src, root+string(filepath.Separator), diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": "src/./main.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sink.entries(); len(got) != 1 || got[0].Path != filepath.Join("src", "main.go") {
		t.Fatalf("contained path events = %+v, want one event for src/main.go", got)
	}
}
