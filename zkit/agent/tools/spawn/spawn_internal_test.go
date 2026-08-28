package spawn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
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
	note := tool.applyPlanner(t.Context(), &args)
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
	note := tool.applyPlanner(t.Context(), &args)
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
	// parallel agent_spawn calls per turn, and one llm round-trip per
	// call adds up fast if we don't gate.
	plan := &fakeSpawnPlanner{}
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}, {Name: "coder"}}}
	args := Args{Prompt: "investigate", Agent: "researcher"}

	note := tool.applyPlanner(t.Context(), &args)
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
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}, {Name: "coder"}}}
	args := Args{Prompt: "find references to Foo", Agent: ""}

	note := tool.applyPlanner(t.Context(), &args)
	if plan.calls != 1 {
		t.Errorf("planner.Plan calls = %d, want 1", plan.calls)
	}
	if args.Agent != "researcher" {
		t.Errorf("args.Agent = %q, want researcher (planner's pick)", args.Agent)
	}
	if !strings.Contains(note, "researcher") {
		t.Errorf("note = %q, want it to mention the chosen agent", note)
	}
	if !strings.Contains(note, "investigation task") {
		t.Errorf("note = %q, want it to include the planner's rationale", note)
	}
}

func TestSpawnMaxIterations_ConfiguredBudgetWins(t *testing.T) {
	t.Parallel()
	tool := New(nil, WithSpawnMaxIterations(20))

	for _, modelSpec := range []int{0, 5, 20, 50} {
		if got := tool.spawnMaxIterations(SpawnModeImplement, modelSpec); got != 20 {
			t.Errorf("spawnMaxIterations(%d) = %d, want configured budget 20", modelSpec, got)
		}
	}
}

func TestSpawnMaxIterations_UnconfiguredPreservesModelValue(t *testing.T) {
	t.Parallel()
	tool := New(nil)

	for _, modelSpec := range []int{0, 5, 50} {
		if got := tool.spawnMaxIterations(SpawnModeImplement, modelSpec); got != modelSpec {
			t.Errorf("spawnMaxIterations(%d) = %d, want %d", modelSpec, got, modelSpec)
		}
	}
}

func TestApplyDefaultAgent_SelectsByMode(t *testing.T) {
	t.Parallel()
	tool := New(nil,
		WithDefaultAgent(SpawnModeExplore, "fast-explorer"),
		WithDefaultAgent(SpawnModeVerify, "test-runner"),
		WithDefaultAgent(SpawnModeImplement, "strong-coder"),
	)

	for _, tc := range []struct {
		name string
		args Args
		want string
	}{
		{name: "explore", args: Args{Mode: "explore"}, want: "fast-explorer"},
		{name: "verify normalized", args: Args{Mode: " VERIFY "}, want: "test-runner"},
		{name: "implement", args: Args{Mode: "implement"}, want: "strong-coder"},
		{name: "omitted mode is implement", args: Args{}, want: "strong-coder"},
		{name: "explicit agent wins", args: Args{Agent: "specialist", Mode: "explore"}, want: "specialist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := tc.args
			tool.applyDefaultAgent(&args)
			if args.Agent != tc.want {
				t.Fatalf("agent = %q, want %q", args.Agent, tc.want)
			}
		})
	}
}

func TestApplyDefaultAgent_UnconfiguredKeepsParentFallback(t *testing.T) {
	t.Parallel()
	args := Args{Mode: "explore"}
	New(nil).applyDefaultAgent(&args)
	if args.Agent != "" {
		t.Fatalf("agent = %q, want empty", args.Agent)
	}
}

func TestResolveTarget_DefaultTargetAndAgentPrecedence(t *testing.T) {
	t.Parallel()
	parent := &runner.Runner{}
	explore := &runner.Runner{}
	named := &runner.Runner{}
	tool := New(parent,
		WithDefaultTarget(SpawnModeExplore, explore),
		WithAgentResolver(func(name string) (*runner.Runner, error) {
			if name == "specialist" {
				return named, nil
			}
			return nil, errors.New("unknown")
		}),
	)

	if got, loaded, notice := tool.resolveTarget(Args{Mode: "explore"}); got != explore || loaded || notice != "" {
		t.Fatalf("unnamed explore target = (%p, %t, %q), want configured target", got, loaded, notice)
	}
	if got, _, _ := tool.resolveTarget(Args{}); got != parent {
		t.Fatalf("unnamed implement target = %p, want parent %p", got, parent)
	}
	if got, loaded, notice := tool.resolveTarget(Args{Agent: "specialist", Mode: "explore"}); got != named || !loaded || notice != "" {
		t.Fatalf("named target = (%p, %t, %q), want named runner", got, loaded, notice)
	}
}

func TestModeMaxIterationsOverridesSharedBudget(t *testing.T) {
	t.Parallel()
	tool := New(nil,
		WithSpawnMaxIterations(20),
		WithModeMaxIterations(SpawnModeExplore, 4),
	)
	if got := tool.spawnMaxIterations(SpawnModeExplore, 50); got != 4 {
		t.Fatalf("explore iterations = %d, want 4", got)
	}
	if got := tool.spawnMaxIterations(SpawnModeImplement, 50); got != 20 {
		t.Fatalf("implement iterations = %d, want shared 20", got)
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
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}, {Name: "coder"}}}
	args := Args{Prompt: "add a method", Agent: "best-coder-ever"}

	note := tool.applyPlanner(t.Context(), &args)
	if plan.calls != 1 {
		t.Errorf("planner.Plan calls = %d, want 1", plan.calls)
	}
	if args.Agent != "coder" {
		t.Errorf("args.Agent = %q, want coder (planner's correction)", args.Agent)
	}
	if !strings.Contains(note, "best-coder-ever") && !strings.Contains(note, "coder") {
		t.Errorf("note = %q, want it to mention the chosen agent", note)
	}
}

func TestApplyPlanner_PlannerErrorFallsThrough(t *testing.T) {
	plan := &fakeSpawnPlanner{err: errors.New("provider down")}
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}}}
	args := Args{Prompt: "task", Agent: ""}

	note := tool.applyPlanner(t.Context(), &args)
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
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}, {Name: "coder"}}}
	args := Args{Prompt: "task", Agent: ""}

	note := tool.applyPlanner(t.Context(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty when planner returned invalid agent", note)
	}
}

func TestFallbackParentSkipsPlanner(t *testing.T) {
	t.Parallel()
	planner := &fakeSpawnPlanner{plan: SpawnPlan{Agent: "researcher", Mode: SpawnModeExplore}}
	tool := New(nil,
		WithSpawnPlannerCandidates(planner, []AgentCandidate{{Name: "researcher"}}),
		WithFallbackPolicy(FallbackParent),
	)
	args := Args{Prompt: "inspect"}
	if tool.fallback == FallbackPlanner {
		_ = tool.applyPlanner(t.Context(), &args)
	}
	if planner.calls != 0 || args.Agent != "" {
		t.Fatalf("planner calls=%d agent=%q, want skipped", planner.calls, args.Agent)
	}
}

func TestApplyPlanner_InvalidModeFallsThrough(t *testing.T) {
	plan := &fakeSpawnPlanner{plan: SpawnPlan{
		Agent: "researcher",
		Mode:  SpawnMode("nope"),
	}}
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}}}
	args := Args{Prompt: "task", Agent: ""}

	note := tool.applyPlanner(t.Context(), &args)
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
	tool := &Tool{planner: plan, plannerAgents: []AgentCandidate{{Name: "researcher"}, {Name: "coder"}}}
	args := Args{Prompt: "double-check this works", Agent: ""}

	note := tool.applyPlanner(t.Context(), &args)
	if note == "" {
		t.Error("note empty; planner should narrate the parent-routing choice")
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

func TestSpawnAdmissionErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		kind tools.Kind
	}{
		{name: "concurrency is budget", err: fmt.Errorf("%w (1)", ErrMaxConcurrent), kind: tools.Kinds.BUDGET},
		{name: "duplicate is validation", err: fmt.Errorf("%w: id", ErrTaskIDExists), kind: tools.Kinds.VALIDATION},
		{name: "closed owner is fatal", err: ErrGroupClosed, kind: tools.Kinds.FATAL},
		{name: "internal admission is fatal", err: errors.New("target nil"), kind: tools.Kinds.FATAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := spawnAdmissionError(tc.err); got.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", got.Kind, tc.kind)
			}
		})
	}
}

func TestGroupCloseUsesSingleRetryableJoin(t *testing.T) {
	t.Parallel()
	group := NewGroup()
	group.wg.Add(1)

	shortCtx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := group.Close(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v, want deadline exceeded", err)
	}
	firstJoined := group.joined
	if err := group.Close(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second bounded Close error = %v, want deadline exceeded", err)
	}
	if group.joined != firstJoined {
		t.Fatal("Close replaced the owned join signal")
	}

	group.wg.Done()
	if err := group.Close(t.Context()); err != nil {
		t.Fatalf("final Close: %v", err)
	}
}

func TestWorkspaceAdmissionErrorClassification(t *testing.T) {
	t.Parallel()
	conflict := fmt.Errorf("%w: writer holds workspace", tools.ErrWorkspaceConflict)
	if got := workspaceAdmissionError(conflict); got.Kind != tools.Kinds.BUDGET {
		t.Fatalf("conflict kind = %s, want budget", got.Kind)
	}
	invalid := errors.New("workspace owner is empty")
	got := workspaceAdmissionError(invalid)
	if got.Kind != tools.Kinds.FATAL || !strings.Contains(got.Error(), "coordinate workspace") {
		t.Fatalf("invalid acquisition = %#v, want wrapped fatal", got)
	}
}

func TestTaskOperationErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		kind tools.Kind
	}{
		{name: "not found", err: fmt.Errorf("%w: missing", ErrTaskNotFound), kind: tools.Kinds.VALIDATION},
		{name: "cancelled", err: context.Canceled, kind: tools.Kinds.TRANSIENT},
		{name: "deadline", err: context.DeadlineExceeded, kind: tools.Kinds.TRANSIENT},
		{name: "internal", err: errors.New("broken task state"), kind: tools.Kinds.FATAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := taskOperationError("agent task", tc.err); got.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", got.Kind, tc.kind)
			}
		})
	}
}
