package sourcechain_test

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type fakeSource struct{}

func (fakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {}
}
func (fakeSource) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true}, nil
}

type namedGuard string

func (g namedGuard) Name() string { return string(g) }

func TestPipelineWrapFixedOrderAndIdentity(t *testing.T) {
	var order []string
	wrap := func(name string) sourcechain.Wrapper {
		return func(src tools.Source) tools.Source {
			order = append(order, name)
			return src
		}
	}
	p := sourcechain.NewPipeline(
		sourcechain.WithDepthFilter(wrap("depth")),
		sourcechain.WithModeFilter(wrap("mode")),
		sourcechain.WithFormatSwitch(wrap("format")),
		sourcechain.WithDiffRecorder(wrap("diff")),
	)
	if got := p.Wrap(fakeSource{}); got == nil {
		t.Fatal("Wrap returned nil")
	}
	want := []string{"diff", "format", "mode", "depth"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("wrapper order = %v, want %v", order, want)
	}

	if got := sourcechain.NewPipeline().Wrap(fakeSource{}); got == nil {
		t.Fatal("identity pipeline returned nil")
	}
}

func TestNewComposesSchemaAndPostSchemaGuardrails(t *testing.T) {
	chain, err := sourcechain.New(fakeSource{}, guardrails.Deps{
		SkillLookup: stubSkills{},
		TestEdit:    guardrails.NewTestEditAdvisory(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := []string{"schema", "shell_policy", "skill_hint", "decompose", "fanout", "test_edit_advisory", "improvement_loop"}
	if !reflect.DeepEqual(chain.GuardrailNames, want) {
		t.Fatalf("names = %v, want %v", chain.GuardrailNames, want)
	}
}

func TestNewRejectsNilAndDuplicateGuards(t *testing.T) {
	_, err := sourcechain.New(fakeSource{}, guardrails.Deps{SkillLookup: stubSkills{}}, sourcechain.WithExtraGuardrails(nil))
	if err == nil {
		t.Fatal("nil guardrail should error")
	}
	_, err = sourcechain.New(fakeSource{}, guardrails.Deps{SkillLookup: stubSkills{}}, sourcechain.WithExtraGuardrails(namedGuard("decompose")))
	if err == nil {
		t.Fatal("duplicate guardrail name should error")
	}
}

type stubSkills struct{}

func (stubSkills) Lookup(string) (string, bool) { return "", false }
