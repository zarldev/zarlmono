package spawn_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestPlannerCannotWidenRequestedAuthority(t *testing.T) {
	for _, async := range []bool{false, true} {
		for _, requested := range []string{"explore", "verify"} {
			for _, agent := range []string{"", "unknown"} {
				name := requested + "/" + agent
				if async {
					name += "/async"
				}
				t.Run(name, func(t *testing.T) {
					group := spawn.NewGroup()
					defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
					probe := &modeProbeTool{}
					mutate := &countTool{name: "mutate", mutates: true}
					reg := tools.NewRegistry()
					if err := reg.Register(probe); err != nil {
						t.Fatal(err)
					}
					if err := reg.Register(mutate); err != nil {
						t.Fatal(err)
					}
					provider := &scriptedProvider{turns: [][]llm.CompletionChunk{{toolCallChunk("mutate", "mutate")}, {toolCallChunk("probe", "probe")}, {textChunk("completed " + requested + " delegation")}}}
					target := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg))
					planner := &recordingPlanner{plan: spawn.SpawnPlan{Mode: spawn.SpawnModeImplement}}
					policy := spawn.WithModeToolPolicy(func(mode spawn.SpawnMode, spec tools.ToolSpec) bool {
						return mode == spawn.SpawnModeImplement || !spec.Mutates
					})
					planning := spawn.WithSpawnPlannerCandidates(planner, []spawn.AgentCandidate{{Name: "worker", Mode: spawn.SpawnModeImplement}})
					var tool tools.Tool = spawn.New(target, policy, planning)
					if async {
						tool = spawn.NewAsync(target, group, policy, planning)
					}
					result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "planned", Arguments: tools.ToolParameters{"prompt": "work", "agent": agent, "mode": requested}})
					if err != nil || !result.Success {
						t.Fatalf("spawn=%v, %v", result, err)
					}
					if async {
						if _, err := group.Wait(t.Context(), spawn.TaskID(result.Data.(map[string]any)["task_id"].(string))); err != nil {
							t.Fatal(err)
						}
					}
					if planner.calls != 1 {
						t.Fatalf("planner calls=%d", planner.calls)
					}
					want, err := taskscope.ParseWorkMode(requested)
					if err != nil {
						t.Fatal(err)
					}
					if mutate.calls != 0 || len(probe.seen) != 1 || probe.seen[0] != want {
						t.Fatalf("planner widened authority: mutations=%d modes=%v", mutate.calls, probe.seen)
					}
				})
			}
		}
	}
}
