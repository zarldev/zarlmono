package guardrails_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestTestEditGuardrail_AnnotatesGoTestEdits(t *testing.T) {
	g := guardrails.NewTestEditAdvisory()
	call := tools.ToolCall{
		ID:        "x",
		ToolName:  "edit",
		Arguments: tools.ToolParameters{"path": "pkg/foo/foo_test.go"},
	}
	result := &tools.ToolResult{Success: true, Data: "edited"}
	if err := g.Inspect(t.Context(), call, result, nil); err != nil {
		t.Fatalf("guardrail rejected: %v", err)
	}
	body, ok := result.Data.(string)
	if !ok {
		t.Fatalf("Data type changed unexpectedly: %T", result.Data)
	}
	if !strings.Contains(body, "advisory") {
		t.Errorf("Data %q missing advisory note", body)
	}
	if !strings.Contains(body, "foo_test.go") {
		t.Errorf("Data %q missing the offending path", body)
	}
}

func TestTestEditGuardrail_AnnotatesApplyPatchTestEdits(t *testing.T) {
	g := guardrails.NewTestEditAdvisory()
	call := tools.ToolCall{
		ID:       "x",
		ToolName: code.ToolNameApplyPatch,
		Arguments: tools.ToolParameters{"patch": `*** Begin Patch
*** Update File: pkg/foo/foo_test.go
@@
-old
+new
*** End Patch`},
	}
	result := &tools.ToolResult{Success: true, Data: "patched"}
	if err := g.Inspect(t.Context(), call, result, nil); err != nil {
		t.Fatalf("guardrail rejected: %v", err)
	}
	body, ok := result.Data.(string)
	if !ok {
		t.Fatalf("Data type changed unexpectedly: %T", result.Data)
	}
	if !strings.Contains(body, "advisory") || !strings.Contains(body, "foo_test.go") {
		t.Errorf("Data %q missing advisory/path", body)
	}
}

func TestTestEditGuardrail_RecognisesPatterns(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"pkg/foo/foo_test.go", true},
		{"src/foo.test.ts", true},
		{"src/foo.test.tsx", true},
		{"src/foo.spec.js", true},
		{"tests/integration.py", true},
		{"FooTest.java", true},
		{"spec/models/user_spec.rb", true},
		{"src/lib/foo.go", false},
		{"README.md", false},
		{"cmd/main.go", false},
		{"docs/test-plan.md", false}, // .md, not a test source file
	}
	g := guardrails.NewTestEditAdvisory()
	for _, tt := range cases {
		t.Run(tt.path, func(t *testing.T) {
			result := &tools.ToolResult{Success: true, Data: "ok"}
			call := tools.ToolCall{
				ID:        "x",
				ToolName:  "edit",
				Arguments: tools.ToolParameters{"path": tt.path},
			}
			_ = g.Inspect(t.Context(), call, result, nil)
			body, _ := result.Data.(string)
			fired := strings.Contains(body, "advisory")
			if fired != tt.want {
				t.Errorf("path %q: fired=%v, want %v (Data=%q)", tt.path, fired, tt.want, body)
			}
		})
	}
}

func TestTestEditGuardrail_SkipsUnwatchedTools(t *testing.T) {
	g := guardrails.NewTestEditAdvisory()
	// read is a pure-read tool; it shouldn't fire the advisory even
	// when reading a test file.
	call := tools.ToolCall{
		ID:        "x",
		ToolName:  "read",
		Arguments: tools.ToolParameters{"path": "foo_test.go"},
	}
	result := &tools.ToolResult{Success: true, Data: "contents"}
	_ = g.Inspect(t.Context(), call, result, nil)
	body, _ := result.Data.(string)
	if strings.Contains(body, "advisory") {
		t.Errorf("read shouldn't fire the advisory; got %q", body)
	}
}

func TestTestEditGuardrail_SkipsFailedResult(t *testing.T) {
	g := guardrails.NewTestEditAdvisory()
	call := tools.ToolCall{
		ID:        "x",
		ToolName:  "edit",
		Arguments: tools.ToolParameters{"path": "foo_test.go"},
	}
	result := &tools.ToolResult{Success: false, Error: "edit failed"}
	_ = g.Inspect(t.Context(), call, result, nil)
	if result.Success {
		t.Errorf("guardrail mutated Success on failure path")
	}
}

func TestTestEditGuardrail_NonStringDataGoesToMetadata(t *testing.T) {
	g := guardrails.NewTestEditAdvisory()
	call := tools.ToolCall{
		ID:        "x",
		ToolName:  "edit",
		Arguments: tools.ToolParameters{"path": "foo_test.go"},
	}
	result := &tools.ToolResult{
		Success: true,
		Data:    map[string]any{"updated": true},
	}
	_ = g.Inspect(t.Context(), call, result, nil)
	if _, ok := result.Data.(map[string]any); !ok {
		t.Fatalf("Data type was rewritten: %T", result.Data)
	}
	if result.Metadata == nil || result.Metadata["advisory"] == nil {
		t.Errorf("non-string Data should put advisory in Metadata; got %+v", result.Metadata)
	}
}

func TestTestEditStrictGuardrail_RejectsFixturesAndTests(t *testing.T) {
	g := guardrails.NewTestEditStrict()
	cases := []string{
		"pkg/foo/foo_test.go",
		"testdata/input.txt",
		"fixtures/input.json",
		"snapshots/view.snap",
		"caddytest/integration/caddyfile_adapt/example.txt",
		"pkg/foo/output.golden",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			call := tools.ToolCall{ID: "x", ToolName: "edit", Arguments: tools.ToolParameters{"path": path}}
			if err := g.Before(t.Context(), call); err == nil {
				t.Fatalf("Before(%q) = nil, want rejection", path)
			}
		})
	}
}

func TestTestEditStrictGuardrail_RejectsApplyPatchTestEdits(t *testing.T) {
	g := guardrails.NewTestEditStrict()
	call := tools.ToolCall{
		ID:       "x",
		ToolName: code.ToolNameApplyPatch,
		Arguments: tools.ToolParameters{"patch": `*** Begin Patch
*** Update File: pkg/foo/foo_test.go
@@
-old
+new
*** End Patch`},
	}
	if err := g.Before(t.Context(), call); err == nil {
		t.Fatal("Before(apply_patch test edit) = nil, want rejection")
	}
}

func TestTestEditStrictGuardrail_RejectsBashTestMutations(t *testing.T) {
	g := guardrails.NewTestEditStrict()
	// Each of these would let a model rewrite or delete a grader's test file
	// without ever touching the write-style tools the file-path branch screens.
	cases := []string{
		"sed -i 's/want 5/want 6/' pkg/foo/foo_test.go",
		"rm pkg/foo/foo_test.go",
		"rm -rf testdata",
		"echo broken > pkg/foo/foo_test.go",
		"git checkout -- pkg/foo/foo_test.go",
		"git rm pkg/foo/foo_test.go",
		"(cd pkg/foo && rm bar_test.go)",
		"/bin/rm foo_test.go",
		"tee pkg/foo/foo_test.go",
		"go build ./... && rm foo_test.go",
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			call := tools.ToolCall{ID: "x", ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": command}}
			if err := g.Before(t.Context(), call); err == nil {
				t.Fatalf("Before(bash %q) = nil, want rejection", command)
			}
		})
	}
}

func TestTestEditStrictGuardrail_AllowsBashReadsAndSourceWrites(t *testing.T) {
	g := guardrails.NewTestEditStrict()
	// These touch no test file: builds/tests, reads, and source mutations are
	// all legitimate in an unattended run.
	cases := []string{
		"go test ./...",
		"cat pkg/foo/foo_test.go",
		"grep -r foo_test .",
		"ls -la testdata",
		"sed -i 's/foo_test/bar/' pkg/foo/foo.go", // source file; script fragment isn't a path
		"rm pkg/foo/foo.go",                       // deleting source isn't gaming the grader's tests
		"echo done > out.log",
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			call := tools.ToolCall{ID: "x", ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": command}}
			if err := g.Before(t.Context(), call); err != nil {
				t.Fatalf("Before(bash %q) = %v, want nil", command, err)
			}
		})
	}
}

func TestTestEditStrictGuardrail_TestFileSubsetOfStrictPath(t *testing.T) {
	cases := []string{
		"pkg/foo/foo_test.go",
		"src/foo.test.tsx",
		"src/foo.spec.js",
		"tests/integration.py",
		"FooTest.java",
		"spec/models/user_spec.rb",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			result := &tools.ToolResult{Success: true, Data: "ok"}
			call := tools.ToolCall{ID: "x", ToolName: "edit", Arguments: tools.ToolParameters{"path": path}}
			_ = guardrails.NewTestEditAdvisory().Inspect(t.Context(), call, result, nil)
			body, _ := result.Data.(string)
			if !strings.Contains(body, "advisory") {
				t.Fatalf("advisory matcher did not fire for %q", path)
			}
			if err := guardrails.NewTestEditStrict().Before(t.Context(), call); err == nil {
				t.Fatalf("strict matcher did not reject advisory-matched path %q", path)
			}
		})
	}
}
