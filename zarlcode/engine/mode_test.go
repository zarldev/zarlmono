package engine_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prompts"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	computertools "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

type fakeTool struct{ name tools.ToolName }

func (t fakeTool) Definition() tools.ToolSpec { return tools.ToolSpec{Name: t.name} }
func (fakeTool) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{}, nil
}

type fakeSource struct{ names []tools.ToolName }

func (s fakeSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		for _, n := range s.names {
			if !yield(fakeTool{n}) {
				return
			}
		}
	}
}

func (fakeSource) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{}, nil // marker: dispatch reached the inner source
}

func listedNames(ctx context.Context, src tools.Source) map[tools.ToolName]bool {
	out := map[tools.ToolName]bool{}
	for t := range src.Tools(ctx) {
		out[t.Definition().Name] = true
	}
	return out
}

func TestModeFilter_PlanRestrictsAndBuildAllows(t *testing.T) {
	inner := fakeSource{names: []tools.ToolName{
		code.ToolNameRead, code.ToolNameWrite, code.ToolNameEdit, code.ToolNameBash,
		code.ToolNameSavePlan, code.ToolNameSavePlanAppend, code.ToolNameUpdatePlan,
		programtools.ToolName, engine.ToolNameListInstructions, engine.ToolNameLoadInstruction, "web_search",
		computertools.ToolNameComputerObserve, dynamic.ToolNameMCPList,
		code.ToolNameBashOutput, code.ToolNameListProcesses,
	}}
	plan := true // toggled below to prove the filter reads it live
	src := engine.NewModeFilteredSource(inner, func() bool { return plan })
	ctx := t.Context()

	// --- PLAN: read-only surface ---
	names := listedNames(t.Context(), src)
	if !names[code.ToolNameRead] || !names["web_search"] {
		t.Errorf("plan: read/web_search should be listed: %v", names)
	}
	for _, planTool := range []tools.ToolName{
		code.ToolNameSavePlan,
		code.ToolNameSavePlanAppend,
		code.ToolNameUpdatePlan,
		programtools.ToolName,
		engine.ToolNameListInstructions,
		engine.ToolNameLoadInstruction,
		computertools.ToolNameComputerObserve,
		dynamic.ToolNameMCPList,
		code.ToolNameBashOutput,
		code.ToolNameListProcesses,
	} {
		if !names[planTool] {
			t.Errorf("plan: read/planning tool %q should be listed: %v", planTool, names)
		}
	}
	if names[code.ToolNameWrite] || names[code.ToolNameEdit] || names[code.ToolNameBash] {
		t.Errorf("plan: mutating tools/bash should be filtered out: %v", names)
	}
	if _, err := src.Execute(ctx, tools.ToolCall{ToolName: code.ToolNameWrite}); err == nil {
		t.Error("plan: dispatching write should error")
	}
	if _, err := src.Execute(ctx, tools.ToolCall{ToolName: code.ToolNameRead}); err != nil {
		t.Errorf("plan: dispatching read should be allowed: %v", err)
	}
	for _, planTool := range []tools.ToolName{
		code.ToolNameSavePlan,
		code.ToolNameSavePlanAppend,
		code.ToolNameUpdatePlan,
		programtools.ToolName,
		engine.ToolNameListInstructions,
		engine.ToolNameLoadInstruction,
		computertools.ToolNameComputerObserve,
		dynamic.ToolNameMCPList,
		code.ToolNameBashOutput,
		code.ToolNameListProcesses,
	} {
		if _, err := src.Execute(ctx, tools.ToolCall{ToolName: planTool}); err != nil {
			t.Errorf("plan: dispatching read/planning tool %q should be allowed: %v", planTool, err)
		}
	}
	// --- BUILD (flip the live flag): full surface ---
	plan = false
	names = listedNames(t.Context(), src)
	if !names[code.ToolNameWrite] || !names[code.ToolNameEdit] || !names[code.ToolNameBash] {
		t.Errorf("build: every tool should be listed: %v", names)
	}
	if _, err := src.Execute(ctx, tools.ToolCall{ToolName: code.ToolNameWrite}); err != nil {
		t.Errorf("build: dispatching write should be allowed: %v", err)
	}
}

func TestModeFilterForwardsForgetTask(t *testing.T) {
	t.Parallel()
	inner := &forgettingToolSource{}
	src := engine.NewModeFilteredSource(inner, func() bool { return false })
	src.ForgetTask("task-1")
	if len(inner.forgotten) != 1 || inner.forgotten[0] != "task-1" {
		t.Fatalf("forgotten = %v", inner.forgotten)
	}
}

type forgettingToolSource struct {
	forgotten []taskscope.ID
}

func (*forgettingToolSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(func(tools.Tool) bool) {}
}

func (*forgettingToolSource) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success("", "ok"), nil
}

func (s *forgettingToolSource) ForgetTask(id taskscope.ID) {
	s.forgotten = append(s.forgotten, id)
}

func TestRenderLivePromptUsesFilteredCuratedTools(t *testing.T) {
	inner := fakeSource{names: []tools.ToolName{
		code.ToolNameRead,
		code.ToolNameWrite,
		code.ToolNameBash,
		spawn.ToolNameAgentSpawn,
	}}
	plan := true
	visible := engine.NewModeFilteredSource(inner, func() bool { return plan })

	// PLAN mode filters the tools delivered to the model (via the tool interface),
	// not the prompt text — the prompt no longer enumerates a roster. Assert the
	// filter on the curated source, and that the prompt carries no tool list.
	planNames := toolNameSet(engine.ToolInfoFromSource(t.Context(), visible))
	if !planNames[code.ToolNameRead] || !planNames[spawn.ToolNameAgentSpawn] {
		t.Fatalf("plan mode dropped a read-only tool: %v", planNames)
	}
	if planNames["write"] || planNames["bash"] {
		t.Fatalf("plan mode leaked a mutating tool: %v", planNames)
	}

	prompt, err := engine.RenderLivePrompt("plan", engine.LivePlanPromptTemplate, "/repo", nil, nil, nil, engine.ToolInfoFromSource(t.Context(), visible), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "**read**") || strings.Contains(prompt, "**write**") {
		t.Fatalf("plan prompt should not enumerate the tool roster:\n%s", prompt)
	}

	plan = false
	buildNames := toolNameSet(engine.ToolInfoFromSource(t.Context(), visible))
	for _, want := range []tools.ToolName{code.ToolNameRead, code.ToolNameWrite, code.ToolNameBash, spawn.ToolNameAgentSpawn} {
		if !buildNames[want] {
			t.Fatalf("build mode missing tool %q: %v", want, buildNames)
		}
	}
}

func toolNameSet(infos []prompts.ToolInfo) map[tools.ToolName]bool {
	m := make(map[tools.ToolName]bool, len(infos))
	for _, i := range infos {
		m[tools.ToolName(i.Name)] = true
	}
	return m
}
