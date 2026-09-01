package spawn_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestExecuteUsesStrictestAuthoredAndProfileMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		profileMode spawn.SpawnMode
		argMode     string
		want        taskscope.WorkMode
	}{
		{name: "argument cannot escalate profile", profileMode: spawn.SpawnModeVerify, argMode: "implement", want: taskscope.WorkModes.VERIFY},
		{name: "argument may tighten profile", profileMode: spawn.SpawnModeImplement, argMode: "explore", want: taskscope.WorkModes.EXPLORE},
		{name: "profile applies without argument", profileMode: spawn.SpawnModeVerify, want: taskscope.WorkModes.VERIFY},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := &scriptedProvider{turns: [][]llm.CompletionChunk{
				{toolCallChunk("probe", "probe")},
				{textChunk("done")},
			}}
			probe := &modeProbeTool{}
			reg := tools.NewRegistry()
			if err := reg.Register(probe); err != nil {
				t.Fatal(err)
			}
			target := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
			tool := spawn.New(nil,
				spawn.WithSpawnPlannerCandidates(nil, []spawn.AgentCandidate{{Name: "worker", Mode: tc.profileMode}}),
				spawn.WithAgentResolver(func(string) (*runner.Runner, error) { return target, nil }),
			)
			args := tools.ToolParameters{"prompt": "work", "agent": "worker"}
			if tc.argMode != "" {
				args["mode"] = tc.argMode
			}
			result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "mode", Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success {
				t.Fatalf("result = %#v", result)
			}
			if len(probe.seen) != 1 || probe.seen[0] != tc.want {
				t.Fatalf("seen modes = %v, want [%v]", probe.seen, tc.want)
			}
		})
	}
}
