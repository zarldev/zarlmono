package guardrails_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestReadBeforeWriteGuardrail_RejectsBlindEdit(t *testing.T) {
	ledger := runner.NewMemoryTaskCallLedger()
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/foo.go"}}
	if err := g.Before(t.Context(), call); err == nil {
		t.Fatal("want rejection for blind edit")
	}
}

func TestReadBeforeWriteGuardrail_AllowsEditAfterRead(t *testing.T) {
	ctx := t.Context()
	ledger := runner.NewMemoryTaskCallLedger()
	ledger.RecordSuccessfulPureCall(ctx, code.ToolNameRead, tools.ToolParameters{"path": "pkg/foo.go"})
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/foo.go"}}
	if err := g.Before(ctx, call); err != nil {
		t.Fatalf("want read to unlock edit, got %v", err)
	}
}

func TestReadBeforeWriteGuardrail_AllowsFollowUpEditAfterEdit(t *testing.T) {
	ctx := t.Context()
	ledger := runner.NewMemoryTaskCallLedger()
	// A prior successful edit is recorded as evidence; a follow-up edit to the
	// same file must not be nagged to re-read (the read → edit → read → edit loop).
	ledger.RecordSuccessfulPureCall(ctx, code.ToolNameEdit, tools.ToolParameters{"path": "pkg/foo.go"})
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/foo.go"}}
	if err := g.Before(ctx, call); err != nil {
		t.Fatalf("want prior edit to unlock follow-up edit, got %v", err)
	}
}

func TestReadBeforeWriteGuardrail_AllowsNewFileWriteWithoutContext(t *testing.T) {
	ledger := runner.NewMemoryTaskCallLedger()
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameWrite, Arguments: tools.ToolParameters{"path": "pkg/new.go"}}
	if err := g.Before(t.Context(), call); err != nil {
		t.Fatalf("new-file write must not require prior context: %v", err)
	}
}

func TestReadBeforeWriteGuardrail_AppendAndPatchRespectExistingFileContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call tools.ToolCall
	}{
		{"append", tools.ToolCall{ToolName: code.ToolNameWriteAppend, Arguments: tools.ToolParameters{"path": "pkg/foo.go"}}},
		{"patch update", tools.ToolCall{ToolName: code.ToolNameApplyPatch, Arguments: tools.ToolParameters{"patch": "*** Begin Patch\n*** Update File: pkg/foo.go\n@@\n-old\n+new\n*** End Patch"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ledger := runner.NewMemoryTaskCallLedger()
			g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
			if err := g.Before(t.Context(), tt.call); err == nil {
				t.Fatal("existing-file mutation without read should be rejected")
			}
			ledger.RecordSuccessfulPureCall(t.Context(), code.ToolNameRead, tools.ToolParameters{"path": "pkg/foo.go"})
			if err := g.Before(t.Context(), tt.call); err != nil {
				t.Fatalf("read should unlock existing-file mutation: %v", err)
			}
		})
	}
}

func TestReadBeforeWriteGuardrail_AllowsPatchAddWithoutContext(t *testing.T) {
	ledger := runner.NewMemoryTaskCallLedger()
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameApplyPatch, Arguments: tools.ToolParameters{"patch": "*** Begin Patch\n*** Add File: pkg/new.go\n+package pkg\n*** End Patch"}}
	if err := g.Before(t.Context(), call); err != nil {
		t.Fatalf("Add File patch must not require prior context: %v", err)
	}
}

func TestReadBeforeWriteGuardrail_EditNudgeRequiresExistingFileRead(t *testing.T) {
	ledger := runner.NewMemoryTaskCallLedger()
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/foo.go"}}
	err := g.Before(t.Context(), call)
	if err == nil {
		t.Fatal("want nudge")
	}
	for _, want := range []string{"existing file", "Call read", "use edit"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("nudge missing %q: %v", want, err)
		}
	}
}

func TestReadBeforeWriteGuardrail_AllowsGoTestPairFallback(t *testing.T) {
	ctx := t.Context()
	ledger := runner.NewMemoryTaskCallLedger()
	ledger.RecordSuccessfulPureCall(ctx, code.ToolNameRead, tools.ToolParameters{"path": "pkg/foo_test.go"})
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameEdit, Arguments: tools.ToolParameters{"path": "pkg/foo.go"}}
	if err := g.Before(ctx, call); err != nil {
		t.Fatalf("want test-pair read to unlock impl edit, got %v", err)
	}
}
