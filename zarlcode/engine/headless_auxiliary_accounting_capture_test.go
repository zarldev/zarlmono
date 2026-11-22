package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func headlessAuxiliaryRows(t *testing.T, store *db.Store, runID string) []db.HeadlessAuxiliaryProviderAccounting {
	t.Helper()
	records, err := store.ListHeadlessAuxiliaryProviderAccounting(t.Context(), runID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]db.HeadlessAuxiliaryProviderAccounting, len(records))
	for i, record := range records {
		rows[i] = record.Accounting
	}
	return rows
}

func headlessAuxiliarySettings(t *testing.T) *engine.Settings {
	t.Helper()
	settings := recordedAuxiliarySettings(t, map[string]string{
		prefs.KeyDecomposeJudge: "on", prefs.KeyCompactionMode: "manual",
		prefs.KeySpawnDefaultExploreProvider:   prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultVerifyProvider:    prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultImplementModel:    "default-model",
	})
	dir := filepath.Join(settings.WorkspaceRoot(), ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture-agent.md"), []byte("---\nname: fixture-agent\ndescription: Auxiliary fixture\nmode: implement\nmodel: named-model\n---\nInspect fixture evidence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestHeadlessAuxiliaryAccountingRecursiveCapture(t *testing.T) {
	for _, routing := range []string{"default siblings", "named recursive", "fallback parent"} {
		t.Run(routing, func(t *testing.T) {
			named := routing == "named recursive"
			settings := headlessAuxiliarySettings(t)
			if routing == "fallback parent" {
				setRecordedAuxiliarySetting(t, settings, prefs.KeySpawnDefaultImplementProvider, "missing-route")
				setRecordedAuxiliarySetting(t, settings, prefs.KeySpawnFallback, "parent")
			}
			p := &auxiliaryRunProvider{recurse: true, siblings: !named, named: named}
			var mu sync.Mutex
			selected := make(map[taskscope.ID]bool)
			provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				if req.ResponseFormat.Name == "" && taskscope.DepthFrom(ctx) > 0 {
					// Actual selected child requests, not excluded descendant MAIN ledger rows.
					for _, message := range req.Messages {
						if message.Role == llm.RoleUser && strings.Contains(message.Content, "auxiliary descendant") {
							mu.Lock()
							selected[taskscope.IDFrom(ctx)] = true
							mu.Unlock()
						}
					}
				}
				stream := p.Complete(ctx, req)
				if req.ResponseFormat.Name != "" {
					return func(yield func(llm.CompletionChunk, error) bool) {
						stream(func(chunk llm.CompletionChunk, err error) bool {
							if chunk.Execution != nil {
								chunk.Execution.GatewayCallID = "DUPLICATE_EXTERNAL_CANARY"
							}
							return yield(chunk, err)
						})
					}
				}
				return stream
			})
			live := recordedAuxiliaryLive(t, settings, provider, engine.WithAuxiliaryObservationSink(func(_ context.Context, o engine.AuxiliaryObservation) { mutateAuxiliary(&o) }))
			live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Model: "actual-model"}, RateCard: recordedAccountingFixture().RateCard})
			live.SetLimits(512, 12, 12, 2)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			result := live.RunHeadlessWithObservations(ctx, "read-only descendants", 12)
			if result.Task.Err != nil {
				t.Fatal(result.Task.Err)
			}
			rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID))
			wantJudge, wantPlanner := 5, 4
			if named {
				wantJudge, wantPlanner = 3, 1
			}
			if routing == "fallback parent" {
				wantPlanner = 0
			}
			if len(rows) != wantJudge+wantPlanner {
				t.Fatalf("rows=%d want=%d", len(rows), wantJudge+wantPlanner)
			}
			mu.Lock()
			children := len(selected)
			mu.Unlock()
			if children < 2 {
				t.Fatalf("actual child request contexts=%d", children)
			}
			ids := make(map[string]bool)
			counts := make(map[engine.AuxiliaryAccountingPurpose]int)
			for _, row := range rows {
				got, err := engine.DecodeRecordedHeadlessAuxiliaryAccounting(row.PayloadJSON)
				if err != nil {
					t.Fatal(err)
				}
				id, err := uuid.Parse(got.InvocationID)
				if err != nil || id.Version() != 4 || id.String() != got.InvocationID || ids[got.InvocationID] {
					t.Fatalf("invocation=%s: %v", got.InvocationID, err)
				}
				ids[got.InvocationID] = true
				counts[got.Purpose]++
				if got.RunID != string(result.Task.ID) || got.Model != "actual-model" || got.Provider != "" || got.StreamOutcome != inference.StreamOutcomes.COMPLETED || got.Usage.PromptTokens != 1000 || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 || got.RateCard == nil || got.ConfiguredEstimate == nil {
					t.Fatalf("capture=%+v", got)
				}
				for _, canary := range []string{"SIDECAR_ONLY_CANARY", "DUPLICATE_EXTERNAL_CANARY", "borrowed-mutated", "missing-auxiliary-file", "auxiliary descendant"} {
					if bytes.Contains(row.PayloadJSON, []byte(canary)) {
						t.Fatalf("private evidence retained: %s", canary)
					}
				}
			}
			if counts[engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE] != wantJudge || counts[engine.AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER] != wantPlanner {
				t.Fatal(counts)
			}
			if result.Auxiliary.Judge == nil || (result.Auxiliary.SpawnPlanner != nil) != (wantPlanner > 0) || len(rows) <= 2 {
				t.Fatal("latest-only slots replaced occurrence history")
			}
			for _, row := range headlessRows(t, settings.Store, string(result.Task.ID)) {
				got := decodeRecordedHeadlessAccounting(t, row.Accounting.PayloadJSON)
				if got.Provider != "" || got.Model != "actual-model" {
					t.Fatal("child MAIN sink unexpectedly populated")
				}
			}
		})
	}
}

func TestHeadlessAuxiliaryAccountingNoStoreAndDisabledControls(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "actual helpers", true: "disabled uniterated"}[disabled], func(t *testing.T) {
			var baseline engine.HeadlessResult
			for _, recorded := range []bool{false, true} {
				settings := headlessAuxiliarySettings(t)
				p := &auxiliaryRunProvider{}
				if disabled {
					setRecordedAuxiliarySetting(t, settings, prefs.KeyDecomposeJudge, "off")
				}
				store := settings.Store
				if !recorded {
					settings.Store = nil
				}
				live := recordedAuxiliaryLive(t, settings, p)
				result := live.RunHeadlessWithObservations(t.Context(), "same helper consumption", 6)
				if !recorded {
					baseline = result
					continue
				}
				rows := headlessAuxiliaryRows(t, store, string(result.Task.ID))
				want := 1
				if disabled {
					want = 0
				}
				if len(rows) != want || result.Task.Reason != baseline.Task.Reason || !cmp.Equal(result.Task.TotalUsage, baseline.Task.TotalUsage) || result.Task.Timing.ProviderAttempts != baseline.Task.Timing.ProviderAttempts || result.Task.Iterations != baseline.Task.Iterations {
					t.Fatalf("recording changed behavior: rows=%d result=%+v raw=%+v", len(rows), result.Task, baseline.Task)
				}
				data, err := json.Marshal(engine.ReportWithAuxiliary(result.Task, result.Auxiliary))
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				if _, ok := fields["run_id"]; ok {
					t.Fatal("new report owner leaked into legacy JSON")
				}
			}
		})
	}
}

// A locked writer keeps process-global slog output safe if unrelated parallel
// tests emit diagnostics while this nonparallel privacy contract is active.
type headlessAuxiliaryLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (w *headlessAuxiliaryLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.Write(p)
}
func (w *headlessAuxiliaryLog) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}

func TestHeadlessAuxiliaryAccountingInvalidEvidencePoisonsCaptureNotTask(t *testing.T) {
	var warnings headlessAuxiliaryLog
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&warnings, nil)))
	defer slog.SetDefault(previous)
	var raw runner.TaskResult
	for _, recorded := range []bool{false, true} {
		settings := headlessAuxiliarySettings(t)
		store := settings.Store
		if !recorded {
			settings.Store = nil
		}
		base := &auxiliaryRunProvider{}
		provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
			if req.ResponseFormat.Name != "decompose_verdict" {
				return base.Complete(ctx, req)
			}
			return func(yield func(llm.CompletionChunk, error) bool) {
				yield(llm.CompletionChunk{Content: "PRIVATE_AUX_POISON_CANARY", PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true, Tokens: -1}}}}, errors.New("https://PRIVATE_AUX_POISON_CANARY.invalid/token"))
			}
		})
		live := recordedAuxiliaryLive(t, settings, provider)
		result := live.RunHeadlessWithObservations(t.Context(), "poisoned auxiliary", 6)
		if !recorded {
			raw = result.Task
			continue
		}
		if result.Task.Reason != raw.Reason || result.Task.Err != nil || raw.Err != nil {
			t.Fatal("capture poison changed task verdict")
		}
		run, err := store.GetHeadlessRun(t.Context(), string(result.Task.ID))
		if err != nil || run.EndedAt != nil {
			t.Fatalf("poison falsely completed run: %+v %v", run, err)
		}
		if rows := headlessAuxiliaryRows(t, store, string(result.Task.ID)); len(rows) != 0 {
			t.Fatal("invalid evidence persisted")
		}
		attempts, err := store.ListHeadlessAttempts(t.Context(), string(result.Task.ID))
		if err != nil || len(attempts) != 0 {
			t.Fatal("poison allowed partial summary", err)
		}
	}
	text := warnings.text()
	if !strings.Contains(text, "accounting capture rejected") || strings.Contains(text, "PRIVATE_AUX_POISON_CANARY") {
		t.Fatalf("unsafe or missing warning: %s", text)
	}
}

func TestHeadlessAuxiliaryAccountingNoCandidatesOrUniteratedHelpers(t *testing.T) {
	settings := recordedAuxiliarySettings(t, map[string]string{
		prefs.KeyDecomposeJudge: "off", prefs.KeyCompactionMode: "manual",
		prefs.KeySpawnDefaultExploreProvider:   prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultVerifyProvider:    prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
	})
	p := &auxiliaryRunProvider{recurse: true}
	live := recordedAuxiliaryLive(t, settings, p)
	live.SetLimits(512, 12, 12, 2)
	result := live.RunHeadlessWithObservations(t.Context(), "no candidates for planner", 12)
	if rows := headlessAuxiliaryRows(t, settings.Store, string(result.Task.ID)); len(rows) != 0 || p.planners != 0 || p.judges != 0 || p.mainCalls == 0 {
		t.Fatalf("noninvoked helpers created rows=%d planner=%d judge=%d main=%d", len(rows), p.planners, p.judges, p.mainCalls)
	}
	live.ClearContext()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	aborted := live.RunHeadlessWithObservations(ctx, "construction cancelled before helper streams", 6)
	if rows := headlessAuxiliaryRows(t, settings.Store, string(aborted.Task.ID)); len(rows) != 0 {
		t.Fatal("cancelled construction fabricated auxiliary invocation")
	}
}
