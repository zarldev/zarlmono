package engine_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestRecordedAuxiliaryScopeCountsMatchRawHelpers(t *testing.T) {
	var raw inference.Snapshot
	for _, recorded := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw", true: "recorded"}[recorded], func(t *testing.T) {
			store := recordedAccountingStore(t)
			p := &auxiliaryRunProvider{recurse: true, siblings: true}
			wire := openai.NewProvider("fixture-key", openai.WithHTTPClient(&http.Client{Transport: auxiliaryWireTransport{provider: p}}), openai.WithResponsesAPI(false))
			live := newAuxiliaryRunner(t, wire, func(context.Context, engine.AuxiliaryObservation) {})
			goal := engine.NewSwarm("same consumption with or without capture")
			if recorded {
				_, err := goal.AddRecorded("member", live, "Exercise actual helpers and descendants.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
					return a.RunRecorded(ctx, store, "session").Err
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := goal.Add("member", live, "Exercise actual helpers and descendants."); err != nil {
					t.Fatal(err)
				}
			}
			startEngineSwarm(t, goal)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := goal.WaitIdle(ctx); err != nil {
				t.Fatal(err)
			}
			got := goal.InferenceSnapshot()
			p.mu.Lock()
			main, judges, planners := p.mainCalls, p.judges, p.planners
			p.mu.Unlock()
			if judges != 5 || planners != 4 || got.Admitted != main+judges+planners || got.Settled != got.Admitted || got.Active != 0 || got.Unknown != 0 || got.Tokens != 3*main+1100*(judges+planners) {
				t.Fatalf("charges %+v main=%d judge=%d planner=%d", got, main, judges, planners)
			}
			_, batch := auxiliaryBatch(t, live)
			if recorded {
				if len(batch.Auxiliaries) != 9 || got != raw {
					t.Fatalf("capture added charges: got=%+v raw=%+v aux=%d", got, raw, len(batch.Auxiliaries))
				}
			} else {
				raw = got
				if len(batch.Auxiliaries) != 0 {
					t.Fatal("non-recorded helpers captured")
				}
			}
		})
	}
}

func TestRecordedAuxiliaryInitializationAndPrerequestFailureDoNotInvokeHelpers(t *testing.T) {
	for _, failure := range []string{"cancelled", "request receipt failure"} {
		t.Run(failure, func(t *testing.T) {
			store := recordedAccountingStore(t)
			p := &auxiliaryRunProvider{recurse: true}
			live := newAuxiliaryRunner(t, p, func(context.Context, engine.AuxiliaryObservation) { t.Error("helper called before root receipt") })
			// Inspection and helper construction enumerate tools without inference.
			_ = live.Inspect(t.Context())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			} else {
				if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_root_request BEFORE INSERT ON session_history_batches BEGIN SELECT RAISE(ABORT,'synthetic request failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_ = live.RunRecordedTurn(ctx, "no request admitted", nil, store, "session")
			p.mu.Lock()
			main, judges, planners := p.mainCalls, p.judges, p.planners
			p.mu.Unlock()
			if main != 0 || judges != 0 || planners != 0 {
				t.Fatalf("pre-root inference %d/%d/%d", main, judges, planners)
			}
			_, batch := auxiliaryBatch(t, live)
			if len(batch.Roots) != 0 || len(batch.Children) != 0 || len(batch.Auxiliaries) != 0 {
				t.Fatal("pre-request operation fabricated accounting")
			}
		})
	}
}

func TestRecordedAuxiliaryNoCandidatesAndDisabledPaths(t *testing.T) {
	store := recordedAccountingStore(t)
	p := &auxiliaryRunProvider{recurse: true}
	settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactionMode: "manual", prefs.KeySpawnDefaultExploreProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultVerifyProvider: prefs.ActiveModelTarget, prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget})
	live := recordedAuxiliaryLive(t, settings, p)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_ = live.RunRecordedTurn(ctx, "empty agent catalog; disabled judge", nil, store, "session")
	_, batch := auxiliaryBatch(t, live)
	if len(batch.Auxiliaries) != 0 || p.judges != 0 || p.planners != 0 {
		t.Fatal("disabled/noninvoked helpers fabricated rows")
	}
	// Adding candidates makes construction possible but does not eagerly invoke
	// the planner. A one-request task that never spawns still records no helper.
	dir := filepath.Join(settings.WorkspaceRoot(), ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate.md"), []byte("---\nname: candidate\ndescription: never invoked\n---\nRead only.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	live.ClearContext()
	p.recurse = false
	live.SetLimits(64, 1, 1, 2)
	_ = live.RunRecordedTurn(ctx, "uniterated planner construction", nil, store, "session")
	_, batch = auxiliaryBatch(t, live)
	if len(batch.Auxiliaries) != 0 || p.planners != 0 {
		t.Fatal("construction alone invoked planner")
	}
}

func TestRecordedAuxiliaryInvalidEvidencePoisonsWholeSnapshotSafely(t *testing.T) {
	store := recordedAccountingStore(t)
	base := &auxiliaryRunProvider{}
	provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		if req.ResponseFormat.Name != "decompose_verdict" {
			return base.Complete(ctx, req)
		}
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "private-unsafe-canary", PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true, Tokens: -1}}}}, errors.New("https://private-unsafe-canary.invalid/token"))
		}
	})
	live := newAuxiliaryRunner(t, provider, func(context.Context, engine.AuxiliaryObservation) {})
	_ = live.RunRecordedTurn(t.Context(), "invalid helper evidence", nil, store, "session")
	history, batch, err := live.RecordedHistoryWithAccountingBatch("session")
	if !errors.Is(err, engine.ErrInvalidRecordedSessionAuxiliaryAccounting) || len(history) != 0 || len(batch.Roots) != 0 || len(batch.Children) != 0 || len(batch.Auxiliaries) != 0 || strings.Contains(err.Error(), "canary") {
		t.Fatalf("unsafe or partial snapshot: %v", err)
	}
}

func TestRecordedAuxiliaryOuterBudgetNeverFabricatesDeniedPlanner(t *testing.T) {
	store := recordedAccountingStore(t)
	p := &auxiliaryRunProvider{recurse: true}
	wire := openai.NewProvider("fixture-key", openai.WithHTTPClient(&http.Client{Transport: auxiliaryWireTransport{provider: p}}), openai.WithResponsesAPI(false))
	live := newAuxiliaryRunner(t, wire, func(context.Context, engine.AuxiliaryObservation) {})
	limits := swarm.DefaultLimits()
	limits.Invocations = 1
	goal := engine.NewSwarm("only root admission", swarm.WithLimits(limits))
	if _, err := goal.AddRecorded("member", live, "single admitted root then stop", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		_ = a.RunRecorded(ctx, store, "session")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	testCtx := t.Context()
	go func() { done <- goal.Run(testCtx) }()
	defer func() {
		_ = goal.Close(context.WithoutCancel(testCtx))
		if err := <-done; !errors.Is(err, swarm.ErrBudget) {
			t.Errorf("budget run: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	_, batch := auxiliaryBatch(t, live)
	if len(batch.Auxiliaries) != 0 || p.planners != 0 || p.judges != 0 {
		t.Fatal("denied helper created evidence")
	}
	if got := goal.InferenceSnapshot(); got.Admitted != 1 || got.Settled != 1 {
		t.Fatal(got)
	}
}

func TestRecordedAuxiliaryCompactorTerminalOutcomes(t *testing.T) {
	for _, kind := range []string{"summary", "executive", "handover"} {
		for _, outcome := range []string{"empty", "failed", "cancelled", "cause", "panic"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				store := recordedAccountingStore(t)
				settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: kind, prefs.KeyCompactionMode: "auto", prefs.KeySpawnEnabled: "off"})
				roots, calls := 0, 0
				ctx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				cause := errors.New("synthetic custom cancellation cause canary")
				provider := recordedAccountingProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
					if len(req.Tools) == 0 {
						return func(yield func(llm.CompletionChunk, error) bool) {
							calls++
							e := auxiliaryEvidence()
							if !yield(llm.CompletionChunk{UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil) {
								return
							}
							if outcome == "failed" {
								yield(llm.CompletionChunk{}, errors.New("synthetic private compactor failure canary"))
							}
							if outcome == "cancelled" {
								yield(llm.CompletionChunk{}, context.DeadlineExceeded)
							}
							if outcome == "cause" {
								cancel(cause)
								yield(llm.CompletionChunk{}, context.Cause(ctx))
							}
							if outcome == "panic" {
								panic(cause)
							}
						}
					}
					roots++
					return func(yield func(llm.CompletionChunk, error) bool) {
						yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 2000, TotalTokens: 2000}, ToolCalls: []llm.ToolCall{{ID: "read-" + string(rune('a'+roots)), Type: "function", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"synthetic-missing"}`}}}}, nil)
					}
				})
				live := recordedAuxiliaryLive(t, settings, provider)
				var contextCache []llm.Message
				for range 20 {
					contextCache = append(contextCache, llm.Message{Role: llm.RoleUser, Content: "older evidence"}, llm.Message{Role: llm.RoleAssistant, Content: "older response"})
				}
				live.RestoreContext(contextCache)
				// Panic recovery belongs to this public caller, not to production.
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					_ = live.RunRecordedTurn(ctx, "compactor terminal stream", nil, store, "session")
				}()
				if outcome == "panic" {
					if recoveredErr, ok := recovered.(error); !ok || !errors.Is(recoveredErr, cause) {
						t.Fatalf("panic = %v", recovered)
					}
				} else if recovered != nil {
					t.Fatalf("unexpected panic = %v", recovered)
				}
				if outcome == "cause" && !errors.Is(context.Cause(ctx), cause) {
					t.Fatal("custom cause lost")
				}
				_, batch := auxiliaryBatch(t, live)
				if len(batch.Auxiliaries) == 0 || len(batch.Auxiliaries) != calls || ((outcome == "panic" || outcome == "cause") && calls != 1) {
					t.Fatalf("compactor invocations=%d captured=%d", calls, len(batch.Auxiliaries))
				}
				want := inference.StreamOutcomes.COMPLETED
				if outcome == "failed" {
					want = inference.StreamOutcomes.FAILED
				}
				if outcome == "cancelled" || outcome == "cause" {
					want = inference.StreamOutcomes.CANCELLED
				}
				if outcome == "panic" {
					want = inference.StreamOutcomes.INTERRUPTED
				}
				for _, row := range batch.Auxiliaries {
					got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(row.PayloadJSON)
					if err != nil || got.StreamOutcome != want || got.Usage == nil || *got.Usage != *auxiliaryEvidence().Usage || got.ReportedCost == nil || got.ReportedCost.Source != "" {
						t.Fatalf("compactor evidence %+v %v", got, err)
					}
				}
			})
		}
	}
}

func TestRecordedAuxiliaryPlannerTerminalOutcomes(t *testing.T) {
	for _, outcome := range []string{"malformed", "empty", "failed", "cancelled", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			store := recordedAccountingStore(t)
			base := &auxiliaryRunProvider{recurse: true}
			calls := 0
			provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				if req.ResponseFormat.Name != "spawn_plan" {
					return base.Complete(ctx, req)
				}
				return func(yield func(llm.CompletionChunk, error) bool) {
					calls++
					e := auxiliaryEvidence()
					content := "private malformed planner response canary"
					if outcome == "empty" {
						content = ""
					}
					if !yield(llm.CompletionChunk{Content: content, UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil) {
						return
					}
					switch outcome {
					case "failed":
						yield(llm.CompletionChunk{}, errors.New("private planner failure canary"))
					case "cancelled":
						yield(llm.CompletionChunk{}, context.Canceled)
					case "panic":
						panic("synthetic planner panic")
					}
				}
			})
			live := newAuxiliaryRunner(t, provider, func(context.Context, engine.AuxiliaryObservation) {})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			_ = live.RunRecordedTurn(ctx, "planner runtime outcomes", nil, store, "session")
			_, batch := auxiliaryBatch(t, live)
			want := inference.StreamOutcomes.COMPLETED
			switch outcome {
			case "failed":
				want = inference.StreamOutcomes.FAILED
			case "cancelled":
				want = inference.StreamOutcomes.CANCELLED
			case "panic":
				want = inference.StreamOutcomes.INTERRUPTED
			}
			captured := 0
			for _, row := range batch.Auxiliaries {
				got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(row.PayloadJSON)
				if err != nil {
					t.Fatal(err)
				}
				if got.Purpose != engine.AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER {
					continue
				}
				captured++
				if got.StreamOutcome != want || got.Usage.PromptTokens != 1000 || got.ReportedCost == nil {
					t.Fatalf("planner evidence %+v", got)
				}
			}
			if calls == 0 || captured != calls {
				t.Fatalf("calls=%d captured=%d", calls, captured)
			}
		})
	}
}
