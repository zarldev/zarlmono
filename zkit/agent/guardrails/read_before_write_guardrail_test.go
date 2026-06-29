package guardrails_test

import (
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

func TestReadBeforeWriteGuardrail_AllowsWriteAfterDirContext(t *testing.T) {
	ctx := t.Context()
	ledger := runner.NewMemoryTaskCallLedger()
	ledger.RecordSuccessfulPureCall(ctx, code.ToolNameLs, tools.ToolParameters{"path": "pkg"})
	g := guardrails.NewReadBeforeWriteGuardrail(ledger, guardrails.ReadBeforeWriteAdvisory)
	call := tools.ToolCall{ToolName: code.ToolNameWrite, Arguments: tools.ToolParameters{"path": "pkg/new.go"}}
	if err := g.Before(ctx, call); err != nil {
		t.Fatalf("want dir listing to unlock new-file write, got %v", err)
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
