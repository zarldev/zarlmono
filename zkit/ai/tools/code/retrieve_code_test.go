package code_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRetrieveCodeToolUsesSyntaxChunksAndStableRanking(t *testing.T) {
	root := t.TempDir()
	writeFileMapFixture(t, root, "alpha.go", `package demo

func Alpha() string { return "alpha" }
func TargetHandler() string { return helperTarget() }
func helperTarget() string { return "target" }
`)
	writeFileMapFixture(t, root, "beta.go", `package demo

func Beta() string { return "beta" }
`)
	writeFileMapFixture(t, root, "alpha_test.go", `package demo

func TestTargetHandler() {}
`)

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	res, err := code.NewRetrieveCodeTool(ws).Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"query": "TargetHandler",
		"limit": 2,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("tool failed: %s", res.Error)
	}
	out := res.Data.(code.RetrieveCodeResult).String()
	if !strings.Contains(out, "retrieve_code: 1 chunk(s)") {
		t.Fatalf("unexpected hit count/output:\n%s", out)
	}
	if !strings.Contains(out, "alpha.go:L4-L4") || !strings.Contains(out, "func TargetHandler() string") {
		t.Fatalf("missing target syntax chunk:\n%s", out)
	}
	if strings.Contains(out, "TestTargetHandler") || strings.Contains(out, "func Alpha") {
		t.Fatalf("retrieval should return only matching non-test syntax chunks:\n%s", out)
	}
}

func TestRetrieveCodeToolCanIncludeTestsAndRenderJSON(t *testing.T) {
	root := t.TempDir()
	writeFileMapFixture(t, root, "main.go", "package main\nfunc Main() {}\n")
	writeFileMapFixture(t, root, "main_test.go", "package main\nfunc TestMain() {}\n")

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	res, err := code.NewRetrieveCodeTool(ws).Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"query":         "TestMain",
		"include_tests": true,
		"output":        "json",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Data.(code.RetrieveCodeResult).String()
	if !strings.Contains(out, `"path":"main_test.go"`) || !strings.Contains(out, `"name":"TestMain"`) {
		t.Fatalf("json output missing test chunk: %s", out)
	}
}
