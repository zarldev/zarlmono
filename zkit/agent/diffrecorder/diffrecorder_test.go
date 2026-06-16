package diffrecorder_test

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// fakeSource implements runner.ToolSource by storing a per-tool-name
// handler. Tools surface their own Definition with the requested name
// so the classifier can route on them.
type fakeSource struct {
	exec map[tools.ToolName]func(ctx context.Context, c tools.ToolCall) (*tools.ToolResult, error)
}

func (f *fakeSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	_ = ctx
	return func(yield func(tools.Tool) bool) {
		for name := range f.exec {
			if !yield(stubTool{name: name}) {
				return
			}
		}
	}
}

func (f *fakeSource) Execute(ctx context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	handler, ok := f.exec[c.ToolName]
	if !ok {
		return &tools.ToolResult{Success: false, Error: "unknown tool"}, nil
	}
	return handler(ctx, c)
}

type stubTool struct{ name tools.ToolName }

func (s stubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name}
}

func (s stubTool) Execute(ctx context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{Success: true}, nil
}

// diffSink collects all (path, diff) callbacks fired by the recorder.
type diffSink struct {
	mu  sync.Mutex
	out []diffEntry
}

type diffEntry struct{ Path, Diff string }

func (d *diffSink) record(path, diff string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.out = append(d.out, diffEntry{path, diff})
}

func (d *diffSink) entries() []diffEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]diffEntry, len(d.out))
	copy(out, d.out)
	return out
}

type eventSink struct {
	mu  sync.Mutex
	out []diffrecorder.DiffEvent
}

func (s *eventSink) record(e diffrecorder.DiffEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.out = append(s.out, e)
}

func (s *eventSink) entries() []diffrecorder.DiffEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]diffrecorder.DiffEvent, len(s.out))
	copy(out, s.out)
	return out
}

// --- recordable: capture diff on modification ---

func TestRecorder_CapturesModificationDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"write": func(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
			rel, _ := c.Arguments["path"].(string)
			return &tools.ToolResult{
					Success: true,
				}, os.WriteFile(
					filepath.Join(dir, rel),
					[]byte("alpha\nBETA\ngamma\ndelta\n"),
					0o644,
				)
		},
	}}
	rec := diffrecorder.New(src, dir, diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": "code.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	entries := sink.entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(entries))
	}
	if entries[0].Path != "code.txt" {
		t.Errorf("path = %q, want code.txt", entries[0].Path)
	}
	body := entries[0].Diff
	for _, want := range []string{"@@ code.txt @@", "-beta", "+BETA", "+delta"} {
		if !strings.Contains(body, want) {
			t.Errorf("diff body missing %q:\n%s", want, body)
		}
	}
}

func TestRecorder_CapturesRichDiffEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &eventSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"write": func(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
			rel, _ := c.Arguments["path"].(string)
			return &tools.ToolResult{Success: true}, os.WriteFile(filepath.Join(dir, rel), []byte("after\n"), 0o644)
		},
	}}
	rec := diffrecorder.NewWithEventSink(src, dir, diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": "code.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	entries := sink.entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 event, got %d", len(entries))
	}
	e := entries[0]
	if e.Path != "code.txt" || !strings.Contains(e.Diff, "-before") || !strings.Contains(e.Diff, "+after") {
		t.Fatalf("unexpected event path/diff: %+v", e)
	}
	if string(e.Before) != "before\n" || e.BeforeMissing {
		t.Fatalf("before image = %q missing=%v, want before image", e.Before, e.BeforeMissing)
	}
	if string(e.After) != "after\n" || e.AfterMissing {
		t.Fatalf("after image = %q missing=%v, want after image", e.After, e.AfterMissing)
	}
}

func TestRecorder_CapturesCompactDiffHunk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	beforeLines := make([]string, 30)
	afterLines := make([]string, 30)
	for i := range beforeLines {
		beforeLines[i] = "line-" + fmtLine(i+1)
		afterLines[i] = beforeLines[i]
	}
	afterLines[19] = "line-020 changed"
	if err := os.WriteFile(path, []byte(strings.Join(beforeLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"edit": func(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
			rel, _ := c.Arguments["path"].(string)
			return &tools.ToolResult{Success: true}, os.WriteFile(
				filepath.Join(dir, rel),
				[]byte(strings.Join(afterLines, "\n")+"\n"),
				0o644,
			)
		},
	}}
	rec := diffrecorder.New(src, dir, diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "edit",
		Arguments: tools.ToolParameters{"path": "long.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}

	entries := sink.entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(entries))
	}
	body := entries[0].Diff
	for _, want := range []string{"@@ long.txt @@", "@@ -17,7 +17,7 @@", "-line-020", "+line-020 changed"} {
		if !strings.Contains(body, want) {
			t.Errorf("diff body missing %q:\n%s", want, body)
		}
	}
	for _, farContext := range []string{"line-001", "line-010", "line-030"} {
		if strings.Contains(body, farContext) {
			t.Errorf("diff should not include far unchanged context %q:\n%s", farContext, body)
		}
	}
}

func fmtLine(n int) string {
	return fmt.Sprintf("%03d", n)
}

// --- recordable: file creation reads as added ---

func TestRecorder_CapturesNewFileAsAdded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"write": func(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
			rel, _ := c.Arguments["path"].(string)
			return &tools.ToolResult{Success: true}, os.WriteFile(filepath.Join(dir, rel), []byte("brand new\n"), 0o644)
		},
	}}
	rec := diffrecorder.New(src, dir, diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": "fresh.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := sink.entries()
	if len(entries) != 1 || !strings.Contains(entries[0].Diff, "+brand new") {
		t.Errorf("expected one diff containing +brand new, got %+v", entries)
	}
}

// --- recordable but no-op write: no callback ---

func TestRecorder_ElidesEmptyDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "same.txt")
	if err := os.WriteFile(path, []byte("identical\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"write": func(_ context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
			// "Re-write" the file with exactly the same content.
			rel, _ := c.Arguments["path"].(string)
			return &tools.ToolResult{Success: true}, os.WriteFile(filepath.Join(dir, rel), []byte("identical\n"), 0o644)
		},
	}}
	rec := diffrecorder.New(src, dir, diffrecorder.NewClassifier(), sink.record)
	_, _ = rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "write",
		Arguments: tools.ToolParameters{"path": "same.txt"},
	})
	if got := sink.entries(); len(got) != 0 {
		t.Errorf("expected no diff callbacks, got %+v", got)
	}
}

// --- pure tool: passthrough, no callback ---

func TestRecorder_PureToolPassesThroughNoCapture(t *testing.T) {
	t.Parallel()
	called := false
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"read": func(_ context.Context, _ tools.ToolCall) (*tools.ToolResult, error) {
			called = true
			return &tools.ToolResult{Success: true}, nil
		},
	}}
	rec := diffrecorder.New(src, t.TempDir(), diffrecorder.NewClassifier(), sink.record)
	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "read",
		Arguments: tools.ToolParameters{"path": "anything"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("pure tool should have been dispatched to the underlying executor")
	}
	if got := sink.entries(); len(got) != 0 {
		t.Errorf("pure tool should not produce a diff callback, got %+v", got)
	}
}

// --- unknown tool: passthrough (no halt anymore) ---

func TestRecorder_UnknownToolPassesThrough(t *testing.T) {
	t.Parallel()
	called := false
	sink := &diffSink{}
	src := &fakeSource{exec: map[tools.ToolName]func(context.Context, tools.ToolCall) (*tools.ToolResult, error){
		"bash": func(_ context.Context, _ tools.ToolCall) (*tools.ToolResult, error) {
			called = true
			return &tools.ToolResult{Success: true}, nil
		},
	}}
	rec := diffrecorder.New(src, t.TempDir(), diffrecorder.NewClassifier(), sink.record)

	_, err := rec.Execute(t.Context(), tools.ToolCall{
		ToolName:  "bash",
		Arguments: tools.ToolParameters{"cmd": "echo hi"},
	})
	if err != nil {
		t.Fatalf("unexpected error from passthrough: %v", err)
	}
	if !called {
		t.Error("unknown tool should still run — observability must not block the agent")
	}
	if got := sink.entries(); len(got) != 0 {
		t.Errorf("unknown tool should produce no diff callback, got %+v", got)
	}
}
