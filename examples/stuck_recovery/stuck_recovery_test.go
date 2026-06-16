package main

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TestDecomposeEscalation_PassAdviseFatal is the integration check behind
// the README's "pass → advise → fatal" claim: driving the SAME failing
// grep through the guarded source four times within one task must
// escalate — silent pass-through on 1-2, a judge-shaped advisory on 3,
// and a fatal stop on 4 — rather than failing identically every time.
func TestDecomposeEscalation_PassAdviseFatal(t *testing.T) {
	fs := NewFileSystem()
	attempts := NewSearchAttempts()
	reg := tools.NewRegistry()
	reg.Register(&grepTool{fs: fs, attempts: attempts})
	source := guardrails.NewGuardedSource(reg, BuildGuardrails(fs, nil)...)

	ctx := taskscope.WithID(t.Context(), "stuck-test")
	call := tools.ToolCall{
		ID:        "g",
		ToolName:  ToolGrep,
		Arguments: tools.ToolParameters{"pattern": "NonExistentHandler"},
	}

	exec := func() *tools.ToolResult {
		res, err := source.Execute(ctx, call)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Success {
			t.Fatal("grep for a missing function should fail")
		}
		return res
	}

	// Failures 1 and 2 pass through untouched — no editorialising.
	for i := 1; i <= 2; i++ {
		if got := exec().Error; strings.Contains(got, "advisory") {
			t.Errorf("failure %d should pass through, got advisory: %q", i, got)
		}
	}
	// Failure 3 → advisory shaped by searchVerdictJudge (spawn a researcher).
	if got := exec().Error; !strings.Contains(got, "advisory") || !strings.Contains(got, "researcher") {
		t.Errorf("failure 3 should carry the judge's spawn-a-researcher advisory, got: %q", got)
	}
	// Failure 4 → fatal: the advisory was ignored, stop retrying this call.
	if got := exec().Error; !strings.Contains(got, "despite advisory") {
		t.Errorf("failure 4 should be fatal, got: %q", got)
	}
}

// TestSearchVerdictJudge_Grep recommends spawn for grep failures.
func TestSearchVerdictJudge_Grep(t *testing.T) {
	judge := &searchVerdictJudge{}
	verdict, err := judge.Judge(t.Context(), guardrails.VerdictInput{
		Tool:  ToolGrep,
		Args:  tools.ToolParameters{"pattern": "NonExistentHandler"},
		Error: "pattern not found",
	})
	if err != nil {
		t.Fatalf("Judge error: %v", err)
	}
	if verdict.Action != guardrails.ActionSpawnSubagent {
		t.Errorf("expected ActionSpawnSubagent, got %s", verdict.Action)
	}
	if verdict.Rationale == "" {
		t.Error("expected non-empty rationale")
	}
}

// TestSearchVerdictJudge_Other suggests smaller scope for other tools.
func TestSearchVerdictJudge_Other(t *testing.T) {
	judge := &searchVerdictJudge{}
	verdict, err := judge.Judge(t.Context(), guardrails.VerdictInput{
		Tool:  ToolListFiles,
		Args:  tools.ToolParameters{},
		Error: "some error",
	})
	if err != nil {
		t.Fatalf("Judge error: %v", err)
	}
	if verdict.Action != guardrails.ActionSmallerScope {
		t.Errorf("expected ActionSmallerScope, got %s", verdict.Action)
	}
}

// TestFileSystem_Grep finds patterns correctly.
func TestFileSystem_Grep(t *testing.T) {
	fs := NewFileSystem()

	// Should find ExistingHandler
	matches := fs.Grep("ExistingHandler")
	if len(matches) == 0 {
		t.Error("expected to find ExistingHandler")
	}

	// Should not find NonExistentHandler
	matches = fs.Grep("NonExistentHandler")
	if len(matches) != 0 {
		t.Errorf("expected no matches, got %d", len(matches))
	}
}

// TestFileSystem_HasFunction detects function existence.
func TestFileSystem_HasFunction(t *testing.T) {
	fs := NewFileSystem()

	if !fs.HasFunction("ExistingHandler") {
		t.Error("expected ExistingHandler to exist")
	}
	if fs.HasFunction("NonExistentHandler") {
		t.Error("expected NonExistentHandler to not exist")
	}
}

// TestSearchAttempts_TracksAttempts records search patterns.
func TestSearchAttempts_TracksAttempts(t *testing.T) {
	attempts := NewSearchAttempts()

	attempts.Record("pattern1")
	attempts.Record("pattern2")

	if attempts.Count() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Count())
	}

	patterns := attempts.Patterns()
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns, got %d", len(patterns))
	}
}

// TestGrepTool_Execute returns not found for missing pattern.
func TestGrepTool_Execute(t *testing.T) {
	fs := NewFileSystem()
	attempts := NewSearchAttempts()
	tool := &grepTool{fs: fs, attempts: attempts}

	result, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test-1",
		ToolName:  ToolGrep,
		Arguments: tools.ToolParameters{"pattern": "NonExistentHandler"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for non-existent pattern")
	}
	if attempts.Count() != 1 {
		t.Errorf("expected 1 attempt recorded, got %d", attempts.Count())
	}
}

// TestGrepTool_Execute returns success for existing pattern.
func TestGrepTool_Execute_Success(t *testing.T) {
	fs := NewFileSystem()
	attempts := NewSearchAttempts()
	tool := &grepTool{fs: fs, attempts: attempts}

	result, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "test-1",
		ToolName:  ToolGrep,
		Arguments: tools.ToolParameters{"pattern": "ExistingHandler"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %v", result.Error)
	}
}
