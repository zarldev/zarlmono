package guardrails_test

import (
	"context"
	"iter"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// emptySource satisfies guardrails.Source with no tools and a no-op Execute.
type emptySource struct{}

func (emptySource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	_ = ctx
	return func(yield func(tools.Tool) bool) {}
}
func (emptySource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "ok"}, nil
}

// stubSkillLookup satisfies the guardrails.SkillLookup interface.
type stubSkillLookup struct{}

func (stubSkillLookup) Lookup(toolName string) (string, bool) { return "", false }

// TestLegacyGuardrailOrder_Golden captures the exact guardrail composition
// order from zarlcode/tui's wrapWithGuardrails (shell.go:1338–1348) before
// the composability refactor. This is the characterisation test — any
// refactor must produce the same Names() output (with the documented
// exceptions: headless mode shows "test_edit_strict" instead of
// "test_edit_advisory" after the split, and "logging" left the chain when
// tool-call audit moved to the sink edge — the runner's ToolStarted /
// ToolCompleted / ToolFailed events already carry the final outcome).
func TestLegacyGuardrailOrder_Golden(t *testing.T) {
	// Build the legacy stack exactly as wrapWithGuardrails does it today.
	source := emptySource{}

	schema := guardrails.NewSchemaGuardrail(source)
	shellPolicy := guardrails.NewShellGuardrail("bash")
	skillHint := guardrails.NewSkillHintGuardrail(stubSkillLookup{})
	decompose := guardrails.NewDecomposeGuardrail(0)
	fanout := guardrails.NewFanoutGuardrail(map[tools.ToolName]int{
		"read":        30,
		"ls":          20,
		"grep":        30,
		"glob":        20,
		"spawn_agent": 3,
	})

	testEdit := guardrails.NewTestEditAdvisory()
	improvement := guardrails.NewImprovementGuardrail("", nil)

	guarded := guardrails.NewGuardedSource(
		source,
		schema,
		shellPolicy,
		skillHint,
		decompose,
		fanout,
		testEdit,
		improvement,
	)

	got := guarded.Names()

	// Golden order — interactive mode (headless=false).
	goldenInteractive := []string{
		"schema",
		"shell_policy",
		"skill_hint",
		"decompose",
		"fanout",
		"test_edit_advisory",
		"improvement_loop",
	}

	if !slices.Equal(got, goldenInteractive) {
		t.Errorf("interactive guardrail order mismatch.\n got:  %v\n want: %v", got, goldenInteractive)
	}

	headlessTestEdit := guardrails.NewTestEditStrict()
	guardedHeadless := guardrails.NewGuardedSource(
		source,
		schema,
		shellPolicy,
		skillHint,
		decompose,
		fanout,
		headlessTestEdit,
		improvement,
	)

	gotHeadless := guardedHeadless.Names()

	goldenHeadless := []string{
		"schema",
		"shell_policy",
		"skill_hint",
		"decompose",
		"fanout",
		"test_edit_strict",
		"improvement_loop",
	}

	if !slices.Equal(gotHeadless, goldenHeadless) {
		t.Errorf("headless guardrail order mismatch.\n got:  %v\n want: %v", gotHeadless, goldenHeadless)
	}

	// Headless now shows the strict guardrail name; the old single type reported
	// test_edit_advisory even when it hard-rejected edits.
}

// TestLegacyGuardrailOrder_ExecutionOrder verifies that the execution order
// (Before pass, then Inspect pass) matches the installation order. This pins
// the behaviour that pre-call guardrails fire in sequence, then dispatch,
// then post-call guardrails fire in sequence.
func TestLegacyGuardrailOrder_ExecutionOrder(t *testing.T) {
	var order []string

	// Build a minimal stack with one dual guardrail before and one after
	// a no-op inner source. The order of calls is what matters.
	first := guardrails.GuardrailFunc{
		GuardName: "first",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, _ error) error {
			order = append(order, "first_inspect")
			return nil
		},
	}
	second := guardrails.GuardrailFunc{
		GuardName: "second",
		Fn: func(_ context.Context, _ tools.ToolCall, _ *tools.ToolResult, _ error) error {
			order = append(order, "second_inspect")
			return nil
		},
	}

	guarded := guardrails.NewGuardedSource(emptySource{}, first, second)
	_, _ = guarded.Execute(t.Context(), tools.ToolCall{
		ID:        "x",
		ToolName:  "test",
		Arguments: tools.ToolParameters{},
	})

	if len(order) != 2 {
		t.Fatalf("expected 2 inspect calls, got %d: %v", len(order), order)
	}
	if order[0] != "first_inspect" || order[1] != "second_inspect" {
		t.Errorf("unexpected inspect order: %v, want [first_inspect second_inspect]", order)
	}
}
