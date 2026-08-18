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

func TestRetrieveCodeToolLabelsAndOrdersEvidenceRoles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFileMapFixture(t, root, "target.go", `package demo
func TargetHandler() string { return "target" }
func UsesTargetHandler() string { return TargetHandler() }
`)
	writeFileMapFixture(t, root, "target_test.go", `package demo
func TestTargetHandler() { _ = TargetHandler() }
`)

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	res, err := code.NewRetrieveCodeTool(ws).Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"query":         "TargetHandler",
		"include_tests": true,
		"limit":         8,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Data.(code.RetrieveCodeResult).String()
	definition := strings.Index(out, "[definition] target.go:L2-L2")
	test := strings.Index(out, "[test] target_test.go:L2-L2")
	reference := strings.Index(out, "[reference] target.go:L3-L3")
	if definition < 0 || test < 0 || reference < 0 {
		t.Fatalf("missing evidence roles:\n%s", out)
	}
	if definition >= test || test >= reference {
		t.Fatalf("evidence order = definition:%d test:%d reference:%d\n%s", definition, test, reference, out)
	}

	jsonResult := res.Data.(code.RetrieveCodeResult)
	jsonResult.Output = tools.OutputJSON
	jsonBody := jsonResult.String()
	for _, role := range []string{`"role":"definition"`, `"role":"test"`, `"role":"reference"`} {
		if !strings.Contains(jsonBody, role) {
			t.Fatalf("json missing %s: %s", role, jsonBody)
		}
	}
}

func TestRetrieveCodeToolReportsActionableTruncation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFileMapFixture(t, root, "target.go", "package demo\nfunc Target() string { return \"a very long target body\" }\n")

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	res, err := code.NewRetrieveCodeTool(ws).Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"query":               "Target",
		"max_bytes_per_chunk": 20,
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result := res.Data.(code.RetrieveCodeResult)
	out := result.String()
	for _, want := range []string{"raise max_bytes_per_chunk", "read this file range directly"} {
		if !strings.Contains(out, want) {
			t.Fatalf("truncation output missing %q: %s", want, out)
		}
	}
	jsonResult := result
	jsonResult.Output = tools.OutputJSON
	if !strings.Contains(jsonResult.String(), `"truncated":true`) {
		t.Fatalf("json output missing per-chunk truncation: %s", jsonResult.String())
	}
}
