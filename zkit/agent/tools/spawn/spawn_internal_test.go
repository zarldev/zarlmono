package spawn

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeSpawnPlanner is the in-package stand-in for SpawnPlanner that
// lets the internal applyPlanner tests probe behaviour without an
// llm.Provider or an HTTP server. Records the inputs it was called
// with and returns either plan or err verbatim.
type fakeSpawnPlanner struct {
	plan  SpawnPlan
	err   error
	calls int
	last  SpawnPlanInput
}

func (f *fakeSpawnPlanner) Plan(_ context.Context, in SpawnPlanInput) (SpawnPlan, error) {
	f.calls++
	f.last = in
	if f.err != nil {
		return SpawnPlan{}, f.err
	}
	return f.plan, nil
}

func TestApplyPlanner_NoPlannerWired_NoOp(t *testing.T) {
	tool := &Tool{}
	args := Args{Prompt: "do the thing", Agent: "missing"}
	note := tool.applyPlanner(context.Background(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty when no planner wired", note)
	}
	if args.Agent != "missing" || args.Prompt != "do the thing" {
		t.Errorf("args mutated without a planner: %+v", args)
	}
}

func TestApplyPlanner_EmptyAgentNames_NoOp(t *testing.T) {
	// A wired planner with no agent names is configuration nonsense;
	// the tool stays silent rather than handing the planner an empty
	// closed set it can't satisfy.
	plan := &fakeSpawnPlanner{plan: SpawnPlan{Agent: "x", Mode: SpawnModeExplore}}
	tool := &Tool{planner: plan, plannerAgents: nil}
	args := Args{Prompt: "task"}
	note := tool.applyPlanner(context.Background(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty for nil agent list", note)
	}
	if plan.calls != 0 {
		t.Errorf("planner.Plan calls = %d, want 0 (gated out)", plan.calls)
	}
}

func TestApplyPlanner_AgentInRegisteredSet_SkipsPlanner(t *testing.T) {
	// Model picked a valid name — no rerouting needed, planner stays
	// silent. Avoiding the call here matters: spawn fan-out emits 3
	// parallel spawn_agent calls per turn, and one llm round-trip per
	// call adds up fast if we don't gate.
	plan := &fakeSpawnPlanner{}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher", "coder"}}
	args := Args{Prompt: "investigate", Agent: "researcher"}

	note := tool.applyPlanner(context.Background(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty when model picked a registered name", note)
	}
	if plan.calls != 0 {
		t.Errorf("planner.Plan calls = %d, want 0 — recognised name should short-circuit", plan.calls)
	}
	if args.Agent != "researcher" || args.Prompt != "investigate" {
		t.Errorf("args mutated when planner shouldn't have fired: %+v", args)
	}
}

func TestApplyPlanner_EmptyAgent_PlannerReroutes(t *testing.T) {
	plan := &fakeSpawnPlanner{plan: SpawnPlan{
		Agent:     "researcher",
		Mode:      SpawnModeExplore,
		Rationale: "investigation task, read-only",
	}}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher", "coder"}}
	args := Args{Prompt: "find references to Foo", Agent: ""}

	note := tool.applyPlanner(context.Background(), &args)
	if plan.calls != 1 {
		t.Errorf("planner.Plan calls = %d, want 1", plan.calls)
	}
	if args.Agent != "researcher" {
		t.Errorf("args.Agent = %q, want researcher (planner's pick)", args.Agent)
	}
	if !strings.HasPrefix(args.Prompt, "[mode: explore]") {
		t.Errorf("args.Prompt = %q, want it to start with mode prefix", args.Prompt)
	}
	if !strings.Contains(note, "researcher") {
		t.Errorf("note = %q, want it to mention the chosen agent", note)
	}
	if !strings.Contains(note, "investigation task") {
		t.Errorf("note = %q, want it to include the planner's rationale", note)
	}
}

func TestApplyPlanner_UnknownAgent_PlannerReroutes(t *testing.T) {
	// Model emitted a name that's not in the registered set —
	// classic confabulation case. Planner picks a valid one.
	plan := &fakeSpawnPlanner{plan: SpawnPlan{
		Agent:     "coder",
		Mode:      SpawnModeImplement,
		Rationale: "code change",
	}}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher", "coder"}}
	args := Args{Prompt: "add a method", Agent: "best-coder-ever"}

	note := tool.applyPlanner(context.Background(), &args)
	if plan.calls != 1 {
		t.Errorf("planner.Plan calls = %d, want 1", plan.calls)
	}
	if args.Agent != "coder" {
		t.Errorf("args.Agent = %q, want coder (planner's correction)", args.Agent)
	}
	if !strings.HasPrefix(args.Prompt, "[mode: implement]") {
		t.Errorf("args.Prompt = %q, want it to start with [mode: implement]", args.Prompt)
	}
	if !strings.Contains(note, "best-coder-ever") && !strings.Contains(note, "coder") {
		t.Errorf("note = %q, want it to mention the chosen agent", note)
	}
}

func TestApplyPlanner_PlannerErrorFallsThrough(t *testing.T) {
	plan := &fakeSpawnPlanner{err: errors.New("provider down")}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher"}}
	args := Args{Prompt: "task", Agent: ""}

	note := tool.applyPlanner(context.Background(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty on planner error (silent fallback)", note)
	}
	if args.Agent != "" || args.Prompt != "task" {
		t.Errorf("args mutated when planner errored: %+v", args)
	}
}

func TestApplyPlanner_InvalidPlanAgentFallsThrough(t *testing.T) {
	// Defensive: if a provider without grammar support somehow
	// returns an agent not in the closed set, the tool falls back
	// rather than dispatching to an unknown agent.
	plan := &fakeSpawnPlanner{plan: SpawnPlan{
		Agent: "wat",
		Mode:  SpawnModeExplore,
	}}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher", "coder"}}
	args := Args{Prompt: "task", Agent: ""}

	note := tool.applyPlanner(context.Background(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty when planner returned invalid agent", note)
	}
}

func TestApplyPlanner_InvalidModeFallsThrough(t *testing.T) {
	plan := &fakeSpawnPlanner{plan: SpawnPlan{
		Agent: "researcher",
		Mode:  SpawnMode("nope"),
	}}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher"}}
	args := Args{Prompt: "task", Agent: ""}

	note := tool.applyPlanner(context.Background(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty when planner returned invalid mode", note)
	}
}

func TestApplyPlanner_EmptyAgentInPlan_IsValid(t *testing.T) {
	// Empty agent in the plan means "use parent" — that's a
	// deliberate planner choice, not invalid output. The mode still
	// applies and the note still fires.
	plan := &fakeSpawnPlanner{plan: SpawnPlan{
		Agent:     "",
		Mode:      SpawnModeVerify,
		Rationale: "no specialist fits; parent runner handles verify",
	}}
	tool := &Tool{planner: plan, plannerAgents: []string{"researcher", "coder"}}
	args := Args{Prompt: "double-check this works", Agent: ""}

	note := tool.applyPlanner(context.Background(), &args)
	if note == "" {
		t.Error("note empty; planner should narrate the parent-routing choice")
	}
	if !strings.HasPrefix(args.Prompt, "[mode: verify]") {
		t.Errorf("args.Prompt = %q, want [mode: verify] prefix", args.Prompt)
	}
	if !strings.Contains(note, "parent") {
		t.Errorf("note = %q, want it to mention parent routing", note)
	}
}

func TestSpawnMode_Valid(t *testing.T) {
	t.Parallel()
	for _, m := range []SpawnMode{SpawnModeExplore, SpawnModeImplement, SpawnModeVerify} {
		if !m.Valid() {
			t.Errorf("SpawnMode(%q).Valid() = false, want true", m)
		}
	}
	for _, m := range []SpawnMode{"", "wat", "EXPLORE"} {
		if m.Valid() {
			t.Errorf("SpawnMode(%q).Valid() = true, want false", m)
		}
	}
}
