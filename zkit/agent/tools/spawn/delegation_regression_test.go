package spawn_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestDelegationPreservesAncestorAuthority(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, ancestor := range []taskscope.WorkMode{taskscope.WorkModes.NONE, taskscope.WorkModes.EXPLORE, taskscope.WorkModes.VERIFY, taskscope.WorkModes.IMPLEMENT} {
			for _, requested := range []string{"", "explore", "verify", "implement"} {
				name := ancestor.String() + "/" + requested
				if async {
					name += "/async"
				}
				t.Run(name, func(t *testing.T) {
					group := spawn.NewGroup()
					defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
					probe := &modeProbeTool{}
					reg := tools.NewRegistry()
					if err := reg.Register(probe); err != nil {
						t.Fatal(err)
					}
					provider := &scriptedProvider{turns: [][]llm.CompletionChunk{{toolCallChunk("probe", "probe")}, {textChunk("done")}}}
					target := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg))
					var tool tools.Tool = spawn.New(target, spawn.WithMaxDepth(2))
					if async {
						tool = spawn.NewAsync(target, group, spawn.WithMaxDepth(2))
					}
					ctx := taskscope.WithWorkMode(taskscope.WithDepth(t.Context(), 1), ancestor)
					result, err := tool.Execute(ctx, tools.ToolCall{ID: "delegate", Arguments: tools.ToolParameters{"prompt": "work", "mode": requested}})
					if err != nil || !result.Success {
						t.Fatalf("spawn = %v, %v", result, err)
					}
					if async {
						id := spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))
						if _, err := group.Wait(ctx, id); err != nil {
							t.Fatal(err)
						}
					}
					want := taskscope.WorkModes.IMPLEMENT
					if ancestor == taskscope.WorkModes.EXPLORE || requested == "explore" {
						want = taskscope.WorkModes.EXPLORE
					} else if ancestor == taskscope.WorkModes.VERIFY || requested == "verify" {
						want = taskscope.WorkModes.VERIFY
					}
					if len(probe.seen) != 1 || probe.seen[0] != want {
						t.Fatalf("mode = %v, want %v", probe.seen, want)
					}
				})
			}
		}
	}
}

func TestDelegationPreservesCustomGate(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			group := spawn.NewGroup()
			defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
			probe := &modeProbeTool{}
			reg := tools.NewRegistry()
			if err := reg.Register(probe); err != nil {
				t.Fatal(err)
			}
			provider := &scriptedProvider{turns: [][]llm.CompletionChunk{{toolCallChunk("probe", "probe")}, {textChunk("done")}}}
			target := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg))
			policy := spawn.WithModeToolPolicy(func(spawn.SpawnMode, tools.ToolSpec) bool { return true })
			var tool tools.Tool = spawn.New(target, policy)
			if async {
				tool = spawn.NewAsync(target, group, policy)
			}
			ctx := runner.WithToolGate(t.Context(), func(spec tools.ToolSpec) bool { return spec.Name != "probe" })
			result, err := tool.Execute(ctx, tools.ToolCall{ID: "delegate", Arguments: tools.ToolParameters{"prompt": "work"}})
			if err != nil || !result.Success {
				t.Fatalf("spawn = %v, %v", result, err)
			}
			if async {
				if _, err := group.Wait(ctx, spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))); err != nil {
					t.Fatal(err)
				}
			}
			if len(probe.seen) != 0 {
				t.Fatal("child replaced inherited tool restriction")
			}
		})
	}
}

func TestExplicitOnlyBoundParentPrunesObservedResults(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxObserved(1))
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	if _, err := group.Bind(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	ctx := taskscope.WithID(t.Context(), "parent")
	for range 5 {
		id := startScopedTask(t, ctx, spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group))
		if _, err := group.Await(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	retained := group.List()
	if len(retained) != 1 || !retained[0].Observed || retained[0].Admitted {
		t.Fatalf("explicit retention = %#v", retained)
	}
}

func TestOversizedAgentCompletionMakesProgress(t *testing.T) {
	group := spawn.NewGroup()
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	if _, err := group.Bind(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	group.Ready("parent")
	ctx := taskscope.WithID(t.Context(), "parent")
	tool := spawn.NewAsync(runner.New(&immediateClient{content: "done"}), group)
	result, err := tool.Execute(ctx, tools.ToolCall{ID: "large", Arguments: tools.ToolParameters{"prompt": "work", "agent": strings.Repeat("\x01", 70000)}})
	if err != nil || !result.Success {
		t.Fatalf("spawn = %v, %v", result, err)
	}
	id := spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))
	if _, err := group.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	sibling := startScopedTask(t, ctx, tool)
	if _, err := group.Wait(ctx, sibling); err != nil {
		t.Fatal(err)
	}
	ready := group.Ready("parent")
	if len(ready.Inputs) != 2 {
		t.Fatalf("ready count = %d, want both completions", len(ready.Inputs))
	}
	var observation spawn.CompletionObservation
	if err := json.Unmarshal([]byte(ready.Inputs[0].Message.Content), &observation); err != nil {
		t.Fatal(err)
	}
	if !observation.Truncated {
		t.Fatal("clipped agent label not marked truncated")
	}
	refs := []tools.AdmissionReference{ready.Inputs[0].Reference, ready.Inputs[1].Reference}
	group.Admit("parent", refs)
	if !group.Finish("parent") {
		t.Fatal("parent cannot finish after admissions")
	}
}
