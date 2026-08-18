package engine

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/instructions"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type guidanceFakeSource struct {
	result *tools.ToolResult
}

func (s guidanceFakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(func(tools.Tool) bool) {}
}

func (s guidanceFakeSource) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	cloned := *s.result
	return &cloned, nil
}

func TestGuidanceSourceAnnotatesSuccessfulRead(t *testing.T) {
	t.Parallel()
	src := newGuidanceSource(guidanceFakeSource{result: &tools.ToolResult{
		Success:    true,
		Data:       "1:abcd|package runner\n",
		ExecutedAt: time.Now(),
	}}, []instructions.NestedDoc{
		{RelPath: "zkit/AGENTS.md"},
		{RelPath: "zkit/agent/runner/AGENTS.md"},
	})

	result, err := src.Execute(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameRead,
		Arguments: tools.ToolParameters{"path": "zkit/agent/runner/run.go"},
	})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}
	body, ok := result.Data.(string)
	if !ok {
		t.Fatalf("result data type = %T, want string", result.Data)
	}
	for _, want := range []string{
		"Applicable guidance",
		"`zkit/agent/runner/AGENTS.md`",
		"`zkit/AGENTS.md`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("annotated read missing %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "zkit/agent/runner/AGENTS.md") > strings.Index(body, "zkit/AGENTS.md") {
		t.Fatalf("nearest guidance was not first:\n%s", body)
	}
}

func TestGuidanceSourceLeavesUnguidedAndFailedResultsUnchanged(t *testing.T) {
	t.Parallel()
	docs := []instructions.NestedDoc{{RelPath: "pkg/AGENTS.md"}}
	cases := []struct {
		name   string
		call   tools.ToolCall
		result *tools.ToolResult
	}{
		{
			name:   "unguided read",
			call:   tools.ToolCall{ToolName: code.ToolNameRead, Arguments: tools.ToolParameters{"path": "other/file.go"}},
			result: &tools.ToolResult{Success: true, Data: "body"},
		},
		{
			name:   "failed read",
			call:   tools.ToolCall{ToolName: code.ToolNameRead, Arguments: tools.ToolParameters{"path": "pkg/file.go"}},
			result: &tools.ToolResult{Success: false, Error: "nope"},
		},
		{
			name:   "other tool",
			call:   tools.ToolCall{ToolName: code.ToolNameGrep, Arguments: tools.ToolParameters{"path": "pkg"}},
			result: &tools.ToolResult{Success: true, Data: "body"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			src := newGuidanceSource(guidanceFakeSource{result: tt.result}, docs)
			t.Parallel()
			got, err := src.Execute(t.Context(), tt.call)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got.Data != tt.result.Data || got.Error != tt.result.Error {
				t.Fatalf("result changed: got %#v want %#v", got, tt.result)
			}
		})
	}
}

func TestGuidanceSourceAnnotatesRetrieveCodePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\nfunc Target() {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	src := newGuidanceSource(tools.NewRegistry(code.NewRetrieveCodeTool(ws)), []instructions.NestedDoc{{RelPath: "pkg/AGENTS.md"}})
	result, err := src.Execute(t.Context(), tools.ToolCall{
		ToolName:  code.ToolNameRetrieveCode,
		Arguments: tools.ToolParameters{"query": "Target"},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	body, ok := result.Data.(string)
	if !ok {
		t.Fatalf("annotated retrieve data type = %T, want string", result.Data)
	}
	if !strings.Contains(body, "pkg/target.go") || !strings.Contains(body, "`pkg/AGENTS.md`") {
		t.Fatalf("annotated retrieve missing code or guidance:\n%s", body)
	}
}
