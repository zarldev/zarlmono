package spawn_test

import (
	"context"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// modeProbeTool records the work mode its ctx carried when executed.
type modeProbeTool struct {
	seen []taskscope.WorkMode
}

func (m *modeProbeTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "probe", Description: "probe"}
}

func (m *modeProbeTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	m.seen = append(m.seen, taskscope.WorkModeFrom(ctx))
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "ok", ExecutedAt: time.Now()}, nil
}

// The spawn tool plants the child's work mode on the child Run's ctx (via
// taskscope), independent of whether a tool-gate policy is wired — that's
// what lets the shell guardrail's verify profile fire for verify-mode
// sub-agents. A spawn without a mode plants nothing.
func TestExecute_PlantsWorkModeOnChildCtx(t *testing.T) {
	run := func(mode string) taskscope.WorkMode {
		provider := &scriptedProvider{turns: [][]llm.CompletionChunk{
			{toolCallChunk("p1", "probe"), doneChunk()}, // child calls probe
			{textChunk("done"), doneChunk()},            // then completes
		}}
		probe := &modeProbeTool{}
		reg := tools.NewRegistry()
		reg.Register(probe)
		parent := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
		tool := spawn.New(parent, spawn.WithMaxDepth(1))

		args := tools.ToolParameters{"prompt": "probe it"}
		if mode != "" {
			args["mode"] = mode
		}
		if _, err := tool.Execute(context.Background(), tools.ToolCall{ID: "c1", Arguments: args}); err != nil {
			t.Fatalf("Execute(mode=%q): %v", mode, err)
		}
		if len(probe.seen) != 1 {
			t.Fatalf("probe executed %d times, want 1", len(probe.seen))
		}
		return probe.seen[0]
	}

	if got := run("verify"); got != taskscope.WorkModes.VERIFY {
		t.Errorf("verify child saw mode %v, want VERIFY", got)
	}
	if got := run("explore"); got != taskscope.WorkModes.EXPLORE {
		t.Errorf("explore child saw mode %v, want EXPLORE", got)
	}
	if got := run(""); got.IsValid() {
		t.Errorf("modeless child saw mode %v, want the zero NONE", got)
	}
}
