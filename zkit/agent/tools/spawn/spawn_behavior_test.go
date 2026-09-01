package spawn_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type recordingPlanner struct {
	plan       spawn.SpawnPlan
	err        error
	calls      int
	probeCalls int
	probeErr   error
	last       spawn.SpawnPlanInput
}

func (p *recordingPlanner) Plan(_ context.Context, in spawn.SpawnPlanInput) (spawn.SpawnPlan, error) {
	p.calls++
	p.last = in
	return p.plan, p.err
}

func (p *recordingPlanner) Probe(context.Context) error {
	p.probeCalls++
	return p.probeErr
}

func TestExecutePlannerRoutingBehavior(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		agent     string
		plan      spawn.SpawnPlan
		planErr   error
		wantCalls int
		wantAgent string
	}{
		{name: "valid authored agent skips planner", agent: "researcher", wantCalls: 0, wantAgent: "researcher"},
		{name: "omitted agent is routed", plan: spawn.SpawnPlan{Agent: "researcher", Mode: spawn.SpawnModeExplore, Rationale: "read-only research"}, wantCalls: 1, wantAgent: "researcher"},
		{name: "unknown agent is corrected", agent: "invented", plan: spawn.SpawnPlan{Agent: "coder", Mode: spawn.SpawnModeImplement}, wantCalls: 1, wantAgent: "coder"},
		{name: "planner error falls through", planErr: errors.New("provider down"), wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			planner := &recordingPlanner{plan: tc.plan, err: tc.planErr}
			resolved := ""
			tool := spawn.New(nil,
				spawn.WithSpawnPlannerCandidates(planner, []spawn.AgentCandidate{{Name: "researcher"}, {Name: "coder"}}),
				spawn.WithAgentResolver(func(name string) (*runner.Runner, error) {
					resolved = name
					return nil, errors.New("not loaded")
				}),
			)
			_, err := tool.Execute(t.Context(), tools.ToolCall{ID: "route", Arguments: tools.ToolParameters{
				"prompt": "investigate", "agent": tc.agent,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if planner.calls != tc.wantCalls {
				t.Fatalf("Plan calls = %d, want %d", planner.calls, tc.wantCalls)
			}
			if resolved != tc.wantAgent {
				t.Fatalf("resolved agent = %q, want %q", resolved, tc.wantAgent)
			}
		})
	}
}

func TestExecutePlannerProbeRunsOnceBeforeRouting(t *testing.T) {
	t.Parallel()
	planner := &recordingPlanner{}
	tool := spawn.New(nil,
		spawn.WithSpawnPlannerCandidates(planner, []spawn.AgentCandidate{{Name: "researcher"}}),
		spawn.WithAgentResolver(func(string) (*runner.Runner, error) { return nil, errors.New("not loaded") }),
	)
	for range 3 {
		result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "probe", Arguments: tools.ToolParameters{
			"prompt": "inspect", "agent": "researcher",
		}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Success {
			t.Fatal("nil target unexpectedly succeeded")
		}
	}
	if planner.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want 1", planner.probeCalls)
	}
	if planner.calls != 0 {
		t.Fatalf("Plan calls = %d, want 0 for a registered agent", planner.calls)
	}
}

func TestExecuteDefaultAgentAndFallbackPolicy(t *testing.T) {
	t.Parallel()
	resolved := ""
	tool := spawn.New(nil,
		spawn.WithDefaultAgent(spawn.SpawnModeExplore, "researcher"),
		spawn.WithAgentResolver(func(name string) (*runner.Runner, error) {
			resolved = name
			return nil, errors.New("offline")
		}),
		spawn.WithFallbackPolicy(spawn.FallbackParent),
	)
	result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "default", Arguments: tools.ToolParameters{
		"prompt": "inspect", "mode": "explore",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "researcher" {
		t.Fatalf("resolved agent = %q, want researcher", resolved)
	}
	if result.Success || !strings.Contains(result.Error, "parent runner is nil") {
		t.Fatalf("result = %#v, want parent fallback failure after default resolution", result)
	}
}

func TestSpawnModeValid(t *testing.T) {
	t.Parallel()
	for _, mode := range []spawn.SpawnMode{spawn.SpawnModeExplore, spawn.SpawnModeVerify, spawn.SpawnModeImplement} {
		if !mode.Valid() {
			t.Errorf("mode %q is not valid", mode)
		}
	}
	for _, mode := range []spawn.SpawnMode{"", "wat", "EXPLORE"} {
		if mode.Valid() {
			t.Errorf("mode %q is unexpectedly valid", mode)
		}
	}
}
