package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

func auxiliaryEvidence() inference.Observation {
	return inference.Observation{Provider: "aux", Model: "actual", TaskID: "owner", Depth: 2, Duration: time.Second, Outcome: inference.StreamOutcomes.COMPLETED,
		Usage:        &llm.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100},
		PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{CacheRead: llm.TokenQuantity{Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true, Tokens: 4}}}}},
		Execution:    &llm.ExecutionObservation{GatewayDeployment: "SIDECAR_ONLY_CANARY", ReportedCost: llm.ReportedCost{Reported: true}}}
}

func mutateAuxiliary(o *engine.AuxiliaryObservation) {
	o.Inference.Model = "mutated"
	if o.Inference.Usage != nil {
		o.Inference.Usage.PromptTokens = 999999
	}
	if o.Inference.PricingUsage != nil && len(o.Inference.PricingUsage.Input.WritesByLifetime) != 0 {
		o.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999999
	}
	if o.Inference.Execution != nil {
		o.Inference.Execution.GatewayDeployment = "mutated"
	}
}

func TestAuxiliarySnapshotCloneAndReport(t *testing.T) {
	o := engine.AuxiliaryObservation{Kind: engine.AuxiliaryKinds.JUDGE, Inference: auxiliaryEvidence()}
	s := engine.AuxiliarySnapshot{Judge: &o, SpawnPlanner: new(o.Clone())}
	clone := s.Clone()
	mutateAuxiliary(&o)
	if clone.Judge.Inference.Model != "actual" || clone.SpawnPlanner.Inference.Usage.PromptTokens != 1000 {
		t.Fatal("Clone aliases caller")
	}
	main := runner.TaskResult{TotalUsage: &llm.Usage{PromptTokens: 2}, Timing: runner.TaskTiming{ProviderAttempts: 1}, Duration: 2 * time.Second}
	report := engine.ReportWithAuxiliary(main, clone)
	baseline := engine.Report(main)
	without := report
	without.AuxiliaryObservations = engine.HeadlessAuxiliarySnapshot{}
	if !reflect.DeepEqual(without, baseline) {
		t.Fatal("auxiliary evidence changed main report")
	}
	mutateAuxiliary(clone.Judge)
	if report.AuxiliaryObservations.Judge.LegacyUsage.PromptTokens != 1000 || report.AuxiliaryObservations.Judge.StreamOutcome != "completed" || report.AuxiliaryObservations.Judge.ResponseValid {
		t.Fatalf("report = %+v", report.AuxiliaryObservations.Judge)
	}
	if report.AuxiliaryObservations.Judge.Execution.GatewayDeployment != "SIDECAR_ONLY_CANARY" {
		t.Fatal("report aliases source")
	}
	for _, tc := range []struct {
		name     string
		report   engine.HeadlessReport
		wantNull bool
	}{
		{"legacy", baseline, true}, {"explicit", report, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.report)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			var slots map[string]json.RawMessage
			if err := json.Unmarshal(fields["auxiliary_observations"], &slots); err != nil {
				t.Fatal(err)
			}
			if len(slots) != 2 {
				t.Fatalf("slots = %s", fields["auxiliary_observations"])
			}
			for _, key := range []string{"judge", "spawn_planner"} {
				if (string(slots[key]) == "null") != tc.wantNull {
					t.Fatalf("%s = %s", key, slots[key])
				}
			}
		})
	}
	unknown := engine.ReportWithAuxiliary(runner.TaskResult{}, engine.AuxiliarySnapshot{Judge: &engine.AuxiliaryObservation{Inference: inference.Observation{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}}, SpawnPlanner: &engine.AuxiliaryObservation{Inference: inference.Observation{PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}, Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}}}})
	if unknown.AuxiliaryObservations.Judge.NormalizedPricingUsage.Input.Total.Known || !unknown.AuxiliaryObservations.SpawnPlanner.NormalizedPricingUsage.Input.Total.Known || unknown.AuxiliaryObservations.Judge.Execution.ReportedCost.Reported || !unknown.AuxiliaryObservations.SpawnPlanner.Execution.ReportedCost.Reported {
		t.Fatal("unknown and known zero conflated")
	}
	if engine.AuxiliaryKinds.NONE.IsValid() {
		t.Fatal("zero kind is valid")
	}
}

// auxiliaryRunProvider exercises the real guarded source and async spawn tools.
// Helpers have sidecar-only canaries and large usage, main requests report 2+1.
type auxiliaryRunProvider struct {
	depths                      map[taskscope.ID]int
	mu                          sync.Mutex
	calls                       map[taskscope.ID]int
	mainCalls, judges, planners int
	recurse, siblings, named    bool
	invalid                     bool
	panicJudge                  bool
	waitJudge                   chan struct{}
}

func (*auxiliaryRunProvider) Name() string { return "aux-fixture" }
func (p *auxiliaryRunProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.mu.Lock()
		if p.calls == nil {
			p.calls = make(map[taskscope.ID]int)
			p.depths = make(map[taskscope.ID]int)
		}
		helper := req.ResponseFormat.Name
		depth := taskscope.DepthFrom(ctx)
		id := taskscope.IDFrom(ctx)
		switch helper {
		case "decompose_verdict":
			p.judges++
		case "spawn_plan":
			p.planners++
		default:
			p.calls[id]++
			p.depths[id] = depth
			p.mainCalls++
		}
		n := p.calls[id]
		p.mu.Unlock()
		if helper != "" {
			if p.panicJudge && helper == "decompose_verdict" {
				panic("judge fixture panic")
			}
			if p.waitJudge != nil && helper == "decompose_verdict" {
				close(p.waitJudge)
				<-ctx.Done()
				yield(llm.CompletionChunk{}, context.Cause(ctx))
				return
			}
			body := `{"rationale":"fixture","action":"smaller_scope"}`
			if helper == "spawn_plan" {
				body = `{"rationale":"fixture","agent":"","mode":"implement"}`
			}
			if p.invalid {
				body = "invalid fixture JSON"
			}
			e := auxiliaryEvidence()
			yield(llm.CompletionChunk{Content: body, UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil)
			// The observer has to clone DURING yield, before these borrowed aliases change.
			e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 9000
			e.Execution.GatewayDeployment = "borrowed-mutated"
			return
		}
		tool := func(name, args string) llm.ToolCall {
			return llm.ToolCall{ID: fmt.Sprintf("tool-%d-%d", depth, n), Type: "function", Function: llm.ToolCallFunction{Name: name, Arguments: args}}
		}
		var calls []llm.ToolCall
		readStart := 1
		if p.recurse && depth < 2 {
			readStart = 3
			if n == 1 {
				agent := ""
				if p.named && depth == 0 {
					agent = "fixture-agent"
				}
				args, _ := json.Marshal(map[string]string{"prompt": "auxiliary descendant", "mode": "implement", "agent": agent})
				calls = []llm.ToolCall{tool("agent_spawn", string(args))}
				if p.siblings && depth == 0 {
					second := tool("agent_spawn", string(args))
					second.ID += "-second"
					calls = append(calls, second)
				}
			} else if n == 2 {
				for _, message := range req.Messages {
					if message.Role != llm.RoleTool {
						continue
					}
					var receipt struct {
						TaskID string `json:"task_id"`
					}
					if json.Unmarshal([]byte(message.Content), &receipt) == nil && receipt.TaskID != "" {
						args, _ := json.Marshal(map[string]string{"task_id": receipt.TaskID})
						call := tool("agent_await", string(args))
						call.ID += strconv.Itoa(len(calls))
						calls = append(calls, call)
					}
				}
			}
		}
		if n >= readStart && n < readStart+3 {
			calls = []llm.ToolCall{tool("read", `{"path":"missing-auxiliary-file"}`)}
		}
		if p.recurse && depth == 2 && n == 4 {
			calls = []llm.ToolCall{tool("agent_spawn", `{ "prompt": "depth denied", "mode": "implement" }`)}
		}
		if len(calls) != 0 {
			yield(llm.CompletionChunk{ToolCalls: calls, UsageReported: true, Usage: llm.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}}, nil)
			return
		}
		yield(llm.CompletionChunk{Content: "Fixture complete.", UsageReported: true, Usage: llm.Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}}, nil)
	}
}

func newAuxiliaryRunner(t *testing.T, p llm.Provider, callback func(context.Context, engine.AuxiliaryObservation), extra ...options.Option[engine.LiveRunner]) *engine.LiveRunner {
	t.Helper()
	settings := newJudgeTestSettings(t)
	for key, val := range map[string]string{prefs.KeyJudgeProvider: prefs.ActiveModelTarget, prefs.KeyCompactProvider: prefs.ActiveModelTarget, prefs.KeyCompactionMode: "manual", prefs.KeyProgrammaticTools: "off", prefs.KeySpawnDefaultExploreProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultVerifyProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultImplementModel: "default-model"} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, val); err != nil {
			t.Fatal(err)
		}
	}
	root := settings.WorkspaceRoot()
	dir := filepath.Join(root, ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture-agent.md"), []byte("---\nname: fixture-agent\ndescription: Auxiliary fixture\nmode: implement\nmodel: named-model\n---\nInspect fixture evidence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(p, ws, "root-model", append([]options.Option[engine.LiveRunner]{engine.WithSettings(settings), engine.WithAuxiliaryObservationSink(callback)}, extra...)...)
	live.SetLimits(512, 12, 12, 2)
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return live
}

func TestAuxiliaryActualRunnerJudgePlannerAndDescendants(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		recurse, siblings, named bool
		judges, planners         int
	}{
		{name: "ordinary zero account", judges: 1},
		{name: "default recursive", recurse: true, judges: 3, planners: 2},
		{name: "named recursive", recurse: true, named: true, judges: 3, planners: 1},
		{name: "concurrent siblings", recurse: true, siblings: true, judges: 5, planners: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &auxiliaryRunProvider{recurse: tc.recurse, siblings: tc.siblings, named: tc.named}
			var mu sync.Mutex
			var observations []engine.AuxiliaryObservation
			live := newAuxiliaryRunner(t, p, func(_ context.Context, o engine.AuxiliaryObservation) {
				mu.Lock()
				observations = append(observations, o.Clone())
				mu.Unlock()
				mutateAuxiliary(&o)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			result := live.RunHeadlessWithObservations(ctx, "Inspect fixture evidence without modifying files.", 12)
			if result.Task.Err != nil {
				t.Fatal(result.Task.Err)
			}
			if p.judges != tc.judges || p.planners != tc.planners {
				t.Fatalf("helpers judge=%d planner=%d, want %d/%d; result=%+v", p.judges, p.planners, tc.judges, tc.planners, p.calls)
			}
			mu.Lock()
			got := append([]engine.AuxiliaryObservation(nil), observations...)
			mu.Unlock()
			if len(got) != tc.judges+tc.planners {
				t.Fatalf("callbacks=%d", len(got))
			}
			for _, o := range got {
				if o.Inference.TaskID == "" || p.calls[o.Inference.TaskID] == 0 || p.depths[o.Inference.TaskID] != o.Inference.Depth || o.Inference.Depth < 0 || o.Inference.Depth > 2 || o.Inference.Provider != "aux-fixture" || o.Inference.Model != "root-model" || !o.ResponseValid || o.Inference.Outcome != inference.StreamOutcomes.COMPLETED || o.Inference.Usage.PromptTokens != 1000 || o.Inference.Execution.GatewayDeployment != "SIDECAR_ONLY_CANARY" || o.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 {
					t.Fatalf("observation=%+v", o)
				}
				if o.Kind == engine.AuxiliaryKinds.SPAWNPLANNER && o.Inference.Depth == 2 {
					t.Fatal("fabricated depth-denied planner")
				}
			}
			if result.Auxiliary.Judge == nil || result.Auxiliary.Judge.Inference.Model != "root-model" || result.Auxiliary.Judge.Inference.Usage.PromptTokens != 1000 {
				t.Fatalf("returned snapshot=%+v", result.Auxiliary)
			}
			if (result.Auxiliary.SpawnPlanner != nil) != (tc.planners > 0) {
				t.Fatalf("planner slot=%+v", result.Auxiliary.SpawnPlanner)
			}
			if result.Task.TotalUsage.PromptTokens != 2*result.Task.Timing.ProviderAttempts || result.Task.TotalUsage.CompletionTokens != result.Task.Timing.ProviderAttempts {
				t.Fatal("auxiliary usage entered main totals")
			}
			data, err := json.Marshal(result.Task.Messages)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "SIDECAR_ONLY_CANARY") || strings.Contains(string(data), "borrowed-mutated") {
				t.Fatal("sidecar-only canary entered transcript")
			}
			mutateAuxiliary(result.Auxiliary.Judge)
			live.ClearContext()
			p.recurse = false
			fresh := live.RunHeadlessWithObservations(ctx, "Fresh operation.", 1)
			if fresh.Auxiliary.Judge != nil || fresh.Auxiliary.SpawnPlanner != nil {
				t.Fatalf("fresh invocation retained previous slots: %+v", fresh.Auxiliary)
			}
		})
	}
}

func TestAuxiliaryHeadlessInvalidPanicCancelSettlement(t *testing.T) {
	for _, name := range []string{"invalid", "panic", "cancel"} {
		t.Run(name, func(t *testing.T) {
			p := &auxiliaryRunProvider{invalid: name == "invalid", panicJudge: name == "panic"}
			if name == "cancel" {
				p.waitJudge = make(chan struct{})
			}
			live := newAuxiliaryRunner(t, p, func(context.Context, engine.AuxiliaryObservation) {})
			if name == "panic" {
				live.SetEarlyStopCommand([]string{"false"})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "cancel" {
				done := make(chan struct{})
				go func() {
					defer close(done)
					select {
					case <-p.waitJudge:
						cancel()
					case <-ctx.Done():
					}
				}()
				defer func() { cancel(); <-done }()
			}
			result := live.RunHeadlessWithObservations(ctx, "Trigger a real judge.", 8)
			o := result.Auxiliary.Judge
			if o == nil || o.ResponseValid {
				t.Fatalf("observation=%+v", o)
			}
			want := inference.StreamOutcomes.COMPLETED
			if name == "panic" {
				want = inference.StreamOutcomes.INTERRUPTED
			}
			if name == "cancel" {
				want = inference.StreamOutcomes.CANCELLED
			}
			if o.Inference.Outcome != want {
				t.Fatalf("outcome=%s want %s", o.Inference.Outcome, want)
			}
		})
	}
}

type auxiliaryTerminalSink struct {
	runner.NopSink
	ended *atomic.Bool
}

func (s auxiliaryTerminalSink) OnConversationEnded(_ context.Context, e runner.ConversationEnded) {
	if e.Depth == 0 {
		s.ended.Store(true)
	}
}
func (auxiliaryTerminalSink) DiffEvent(diffrecorder.DiffEvent) {}
func (auxiliaryTerminalSink) PlanUpdated(string, code.Plan)    {}

type lateAuxiliaryProvider struct {
	child     auxiliaryRunProvider
	rootCalls int
}

func (*lateAuxiliaryProvider) Name() string { return "late-aux" }
func (p *lateAuxiliaryProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	if taskscope.DepthFrom(ctx) > 0 {
		return p.child.Complete(ctx, req)
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.rootCalls++
		if p.rootCalls == 1 {
			yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "late-spawn", Type: "function", Function: llm.ToolCallFunction{Name: "agent_spawn", Arguments: `{"agent":"fixture-agent","prompt":"late child judge","mode":"implement"}`}}}}, nil)
			return
		}
		select {
		case <-p.child.waitJudge:
		case <-ctx.Done():
			yield(llm.CompletionChunk{}, context.Cause(ctx))
			return
		}
		yield(llm.CompletionChunk{Content: "Root stopped while child judge is still active."}, nil)
	}
}

func TestAuxiliaryHeadlessSnapshotsChildAfterRootTerminalDrain(t *testing.T) {
	p := &lateAuxiliaryProvider{child: auxiliaryRunProvider{waitJudge: make(chan struct{})}}
	var ended, late atomic.Bool
	live := newAuxiliaryRunner(t, p, func(_ context.Context, o engine.AuxiliaryObservation) {
		if o.Kind == engine.AuxiliaryKinds.JUDGE && o.Inference.Depth == 1 && ended.Load() {
			late.Store(true)
		}
	}, engine.WithLiveSink(auxiliaryTerminalSink{ended: &ended}))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := live.RunHeadlessWithObservations(ctx, "Start the owned late child.", 2)
	if !ended.Load() || !late.Load() {
		t.Fatal("did not observe actual child judge settlement after root terminal")
	}
	if result.Auxiliary.Judge == nil || result.Auxiliary.Judge.Inference.Depth != 1 || result.Auxiliary.Judge.Inference.Outcome != inference.StreamOutcomes.CANCELLED {
		t.Fatalf("snapshot before child drain: %+v", result.Auxiliary.Judge)
	}
}

func TestAuxiliaryInspectionNoopAndEmptyCandidates(t *testing.T) {
	p := &auxiliaryRunProvider{recurse: true}
	var callbacks atomic.Int32
	live := newAuxiliaryRunner(t, p, func(context.Context, engine.AuxiliaryObservation) { callbacks.Add(1) })
	ins := live.Inspect(t.Context())
	if _, err := ins.Guardrails.DecomposeJudge.Judge(t.Context(), guardrails.VerdictInput{}); err != nil {
		t.Fatal(err)
	}
	if callbacks.Load() != 0 {
		t.Fatal("inspection used turn callback")
	}
	if err := os.Remove(filepath.Join(ins.WorkspaceRoot, ".zarlcode", "agents", "fixture-agent.md")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := live.RunHeadlessWithObservations(ctx, "No planner candidates.", 10)
	if p.planners != 0 || result.Auxiliary.SpawnPlanner != nil {
		t.Fatal("empty candidates fabricated planner evidence")
	}
	if result.Auxiliary.Judge == nil || callbacks.Load() != 3 {
		t.Fatalf("actual judge callbacks=%d", callbacks.Load())
	}
}
