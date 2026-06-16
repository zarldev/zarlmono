package guardrails_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// fakeSkillLookup is a tiny in-memory SkillLookup used by tests
// that doesn't depend on any filesystem state.
type fakeSkillLookup map[string]string

func (f fakeSkillLookup) Lookup(toolName string) (string, bool) {
	p, ok := f[toolName]
	return p, ok
}

func failedEdit(reason string) *tools.ToolResult {
	return &tools.ToolResult{
		ToolCallID: "c",
		Success:    false,
		Err:        tools.Validation("edit", reason),
		Error:      reason,
	}
}

func TestSkillHintGuardrail_AppendsHintOnFailure(t *testing.T) {
	t.Parallel()
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"edit": "/ws/.zarlcode/skills/edit.md",
	})
	res := failedEdit("old_string not unique")
	call := tools.ToolCall{ID: "c", ToolName: "edit"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatalf("Inspect: want nil (mutation, not rejection), got %v", err)
	}
	if !strings.Contains(res.Error, "old_string not unique") {
		t.Errorf("original error stripped: %q", res.Error)
	}
	if !strings.Contains(res.Error, `read("/ws/.zarlcode/skills/edit.md")`) {
		t.Errorf("hint missing: %q", res.Error)
	}
}

func TestSkillHintGuardrail_SkipsPermissionFailures(t *testing.T) {
	t.Parallel()
	// A Permission failure (e.g. workspace-escape, file-mode denial)
	// isn't fixed by reading the tool's skill — the recovery is a
	// different path or a different user, not a different call shape.
	// The guardrail must leave the result untouched.
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"write": "/ws/.zarlcode/skills/write.md",
	})
	res := &tools.ToolResult{
		ToolCallID: "c",
		Success:    false,
		Err:        tools.Permission("write", "escapes workspace"),
		Error:      "write: permission: escapes workspace",
	}
	call := tools.ToolCall{ID: "c", ToolName: "write"}
	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Error, "skill:") {
		t.Errorf("permission failure should not get a skill hint: %q", res.Error)
	}
}

func TestSkillHintGuardrail_UsesTypedErrWhenPresent(t *testing.T) {
	t.Parallel()
	// Permission-skip path should fire from result.Err — the Err field
	// is the sole source of truth for classification.
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"write": "/ws/.zarlcode/skills/write.md",
	})
	res := &tools.ToolResult{
		ToolCallID: "c",
		Success:    false,
		Err:        tools.Permission("write", "escapes workspace"),
		Error:      "write: permission: escapes workspace",
	}
	call := tools.ToolCall{ID: "c", ToolName: "write"}
	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Error, "skill:") {
		t.Errorf("skill_hint should consult typed Err: %q", res.Error)
	}
}

func TestSkillHintGuardrail_PreservesOriginalKind(t *testing.T) {
	t.Parallel()
	// The whole reason for the mutation-not-rejection pattern: a
	// Kinds.NOTFOUND failure must stay Kinds.NOTFOUND after augmentation,
	// otherwise downstream classifiers see a misleading category.
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"read": "/ws/.zarlcode/skills/read.md",
	})
	res := &tools.ToolResult{
		ToolCallID: "c",
		Success:    false,
		Err:        tools.NotFound("read", "no such file"),
		Error:      "no such file",
	}
	call := tools.ToolCall{ID: "c", ToolName: "read"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.NOTFOUND {
		t.Errorf("Err.Kind = %v, want Kinds.NOTFOUND preserved", res.Err)
	}
}

func TestSkillHintGuardrail_NoSkillNoHint(t *testing.T) {
	t.Parallel()
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"edit": "/ws/.zarlcode/skills/edit.md",
	})
	res := failedEdit("disk full")
	call := tools.ToolCall{ID: "c", ToolName: "write"} // no matching skill

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Error, "skill:") {
		t.Errorf("hint appended despite no matching skill: %q", res.Error)
	}
	if res.Error != "disk full" {
		t.Errorf("error mutated when no skill match: %q", res.Error)
	}
}

func TestSkillHintGuardrail_SuccessIgnored(t *testing.T) {
	t.Parallel()
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"edit": "/ws/.zarlcode/skills/edit.md",
	})
	res := &tools.ToolResult{ToolCallID: "c", Success: true, Data: "ok"}
	call := tools.ToolCall{ID: "c", ToolName: "edit"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if res.Data != "ok" {
		t.Errorf("success result mutated: %v", res.Data)
	}
}

func TestSkillHintGuardrail_EmptyErrorIgnored(t *testing.T) {
	t.Parallel()
	// A failure with no error message has nothing to augment.
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"edit": "/ws/.zarlcode/skills/edit.md",
	})
	res := &tools.ToolResult{ToolCallID: "c", Success: false}
	call := tools.ToolCall{ID: "c", ToolName: "edit"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Errorf("error fabricated for empty failure: %q", res.Error)
	}
}

func TestSkillHintGuardrail_NilResultNoOp(t *testing.T) {
	t.Parallel()
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{"edit": "/p"})
	call := tools.ToolCall{ID: "c", ToolName: "edit"}
	if err := g.Inspect(t.Context(), call, nil, nil); err != nil {
		t.Fatalf("Inspect(nil result): want nil, got %v", err)
	}
}

func TestSkillHintGuardrail_NilLookupNoOp(t *testing.T) {
	t.Parallel()
	// Constructor accepts nil so callers can compose unconditionally.
	g := guardrails.NewSkillHintGuardrail(nil)
	res := failedEdit("old_string not unique")
	call := tools.ToolCall{ID: "c", ToolName: "edit"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if res.Error != "old_string not unique" {
		t.Errorf("error mutated with nil lookup: %q", res.Error)
	}
}

func TestSkillHintGuardrail_Idempotent(t *testing.T) {
	t.Parallel()
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"edit": "/ws/.zarlcode/skills/edit.md",
	})
	res := failedEdit("old_string not unique")
	call := tools.ToolCall{ID: "c", ToolName: "edit"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	first := res.Error
	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if res.Error != first {
		t.Errorf("second Inspect mutated again — expected idempotent.\nfirst:  %q\nsecond: %q", first, res.Error)
	}
	// Sanity: the unique hint marker should appear exactly once. The
	// path itself shows up twice (once in prose, once in the literal
	// read() call) by design.
	if c := strings.Count(res.Error, "(skill:"); c != 1 {
		t.Errorf("hint marker (skill: appears %d times after two Inspects, want 1", c)
	}
}

func TestSkillHintGuardrail_MentionsToolName(t *testing.T) {
	t.Parallel()
	g := guardrails.NewSkillHintGuardrail(fakeSkillLookup{
		"bash": "/ws/.zarlcode/skills/bash.md",
	})
	res := failedEdit("permission denied")
	call := tools.ToolCall{ID: "c", ToolName: "bash"}

	if err := g.Inspect(t.Context(), call, res, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "bash") {
		t.Errorf("hint should name the tool: %q", res.Error)
	}
}
