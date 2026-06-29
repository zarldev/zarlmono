package spawn

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// probingPlanner is a SpawnPlanner that also satisfies ProbingPlanner.
// Records how many times Probe was called and returns probeErr.
type probingPlanner struct {
	fakeSpawnPlanner
	probeErr   error
	probeCalls int
}

func (p *probingPlanner) Probe(context.Context) error {
	p.probeCalls++
	return p.probeErr
}

// captureLogs swaps the default slog logger for one writing to buf for
// the duration of the test, restoring it after.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestApplyPlanner_ProbeWarnsOnFailure(t *testing.T) {
	buf := captureLogs(t)
	p := &probingPlanner{probeErr: errors.New("provider unreachable")}
	tool := &Tool{planner: p, plannerAgents: []string{"researcher"}}

	// Model omitted the agent — the planner's Plan path also runs, but
	// the probe is what we're asserting on here.
	args := Args{Prompt: "task"}
	tool.applyPlanner(t.Context(), &args)

	if p.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want 1", p.probeCalls)
	}
	if logs := buf.String(); !strings.Contains(logs, "planner probe failed") {
		t.Errorf("expected a probe-failure warning, logs = %q", logs)
	}
}

func TestApplyPlanner_ProbeFiresEvenWhenModelPicksValidName(t *testing.T) {
	// The whole point of the probe: a healthy run can pick a valid agent
	// every time and never reach the planner's Plan path. The probe must
	// still fire on the first applyPlanner, before that early return.
	p := &probingPlanner{}
	tool := &Tool{planner: p, plannerAgents: []string{"researcher", "coder"}}
	args := Args{Prompt: "investigate", Agent: "researcher"} // valid → Plan skipped

	note := tool.applyPlanner(t.Context(), &args)
	if note != "" {
		t.Errorf("note = %q, want empty (valid name short-circuits Plan)", note)
	}
	if p.calls != 0 {
		t.Errorf("Plan calls = %d, want 0 (valid name)", p.calls)
	}
	if p.probeCalls != 1 {
		t.Errorf("Probe calls = %d, want 1 — probe must precede the valid-name early return", p.probeCalls)
	}
}

func TestApplyPlanner_ProbeRunsOnce(t *testing.T) {
	p := &probingPlanner{}
	tool := &Tool{planner: p, plannerAgents: []string{"researcher"}}
	for range 3 {
		args := Args{Prompt: "task", Agent: "researcher"}
		tool.applyPlanner(t.Context(), &args)
	}
	if p.probeCalls != 1 {
		t.Errorf("Probe calls = %d across 3 applyPlanner calls, want 1 (sync.Once)", p.probeCalls)
	}
}

func TestApplyPlanner_NonProbingPlanner_NoPanic(t *testing.T) {
	// A planner that doesn't implement ProbingPlanner: the probe type
	// assertion fails, no probe runs, nothing panics.
	p := &fakeSpawnPlanner{plan: SpawnPlan{Agent: "researcher", Mode: SpawnModeExplore}}
	tool := &Tool{planner: p, plannerAgents: []string{"researcher"}}
	args := Args{Prompt: "task"}
	tool.applyPlanner(t.Context(), &args) // must not panic
}
