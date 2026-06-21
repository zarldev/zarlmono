package engine

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
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

func listedNames(src tools.Source) map[tools.ToolName]bool {
	out := map[tools.ToolName]bool{}
	for t := range src.Tools(context.Background()) {
		out[t.Definition().Name] = true
	}
	return out
}

func TestModeFilter_PlanRestrictsAndBuildAllows(t *testing.T) {
	inner := fakeSource{names: []tools.ToolName{
		code.ToolNameRead, code.ToolNameWrite, code.ToolNameEdit, code.ToolNameBash, "web_search",
	}}
	plan := true // toggled below to prove the filter reads it live
	src := NewModeFilteredSource(inner, func() bool { return plan })
	ctx := context.Background()

	// --- PLAN: read-only surface ---
	names := listedNames(src)
	if !names[code.ToolNameRead] || !names["web_search"] {
		t.Errorf("plan: read/web_search should be listed: %v", names)
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
	// --- BUILD (flip the live flag): full surface ---
	plan = false
	names = listedNames(src)
	if !names[code.ToolNameWrite] || !names[code.ToolNameEdit] || !names[code.ToolNameBash] {
		t.Errorf("build: every tool should be listed: %v", names)
	}
	if _, err := src.Execute(ctx, tools.ToolCall{ToolName: code.ToolNameWrite}); err != nil {
		t.Errorf("build: dispatching write should be allowed: %v", err)
	}
}

func TestRenderLivePromptUsesFilteredCuratedTools(t *testing.T) {
	inner := fakeSource{names: []tools.ToolName{
		code.ToolNameRead,
		code.ToolNameWrite,
		code.ToolNameBash,
		"spawn_agent",
	}}
	plan := true
	visible := NewModeFilteredSource(inner, func() bool { return plan })

	// PLAN mode filters the tools delivered to the model (via the tool interface),
	// not the prompt text — the prompt no longer enumerates a roster. Assert the
	// filter on the curated source, and that the prompt carries no tool list.
	planNames := toolNameSet(ToolInfoFromSource(t.Context(), visible))
	if !planNames["read"] || !planNames["spawn_agent"] {
		t.Fatalf("plan mode dropped a read-only tool: %v", planNames)
	}
	if planNames["write"] || planNames["bash"] {
		t.Fatalf("plan mode leaked a mutating tool: %v", planNames)
	}

	prompt, err := RenderLivePrompt("plan", LivePlanPromptTemplate, "/repo", nil, nil, nil, ToolInfoFromSource(t.Context(), visible))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "**read**") || strings.Contains(prompt, "**write**") {
		t.Fatalf("plan prompt should not enumerate the tool roster:\n%s", prompt)
	}

	plan = false
	buildNames := toolNameSet(ToolInfoFromSource(t.Context(), visible))
	for _, want := range []tools.ToolName{"read", "write", "bash", "spawn_agent"} {
		if !buildNames[want] {
			t.Fatalf("build mode missing tool %q: %v", want, buildNames)
		}
	}
}

func toolNameSet(infos []promptTool) map[tools.ToolName]bool {
	m := make(map[tools.ToolName]bool, len(infos))
	for _, i := range infos {
		m[tools.ToolName(i.Name)] = true
	}
	return m
}
