package engine_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/options"
)

func headlessAccountingLive(t *testing.T, provider llm.Provider, opts ...options.Option[engine.LiveRunner]) (*engine.LiveRunner, *db.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "headless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, root)
	opts = append([]options.Option[engine.LiveRunner]{engine.WithSettings(settings)}, opts...)
	live := requestControlRunner(t, provider, root, opts...)
	return live, store, root
}

func headlessRows(t *testing.T, store *db.Store, runID string) []db.HeadlessProviderAccountingRecord {
	t.Helper()
	rows, err := store.ListHeadlessProviderAccounting(t.Context(), runID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestHeadlessAccountingMultipleRunsRedrivesAndProviderOrdinals(t *testing.T) {
	fixture := recordedAccountingFixture()
	calls := 0
	var root string
	provider := recordedAccountingProvider(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		calls++
		call := calls
		return func(yield func(llm.CompletionChunk, error) bool) {
			chunk := llm.CompletionChunk{UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage,
				Execution: &llm.ExecutionObservation{GatewayCallID: "duplicate-id-private", GatewayDeployment: "deployment-private", ReportedCost: llm.ReportedCost{Reported: true, Amount: 17, Currency: "JPY", Source: "source-private"}}}
			for range 2 {
				if !yield(chunk, nil) {
					return
				}
			}
			if call == 1 {
				yield(llm.CompletionChunk{}, &llm.RateLimitError{Retryable: true, RetryAfter: time.Nanosecond})
				return
			}
			if call == 3 {
				if err := os.WriteFile(filepath.Join(root, "verified"), nil, 0o600); err != nil {
					t.Error(err)
				}
			}
			yield(llm.CompletionChunk{Content: "done", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	live, store, workspace := headlessAccountingLive(t, provider)
	root = workspace
	if out, err := exec.CommandContext(t.Context(), "git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "actual route", Model: "actual model"}, RateCard: fixture.RateCard})
	live.SetVerifyLoop("test -f verified", 2)
	first := live.RunHeadless(t.Context(), "private prompt", 4)
	if first.Err != nil || first.Reason != runner.TerminalCompleted {
		t.Fatalf("first drive: %+v", first)
	}
	rows := headlessRows(t, store, string(first.ID))
	if len(rows) != 3 {
		t.Fatalf("rows=%d calls=%d", len(rows), calls)
	}
	wantAttempts, wantOrdinals := []int{1, 1, 2}, []int{1, 2, 1}
	for i, row := range rows {
		got := decodeRecordedHeadlessAccounting(t, row.Accounting.PayloadJSON)
		if got.RunID != string(first.ID) || got.PursueAttempt != wantAttempts[i] || got.ProviderOrdinal != wantOrdinals[i] || got.Provider != "actual route" || got.Model != "actual model" || !reflect.DeepEqual(got.Usage, fixture.Usage) || !reflect.DeepEqual(got.ConfiguredEstimate, fixture.ConfiguredEstimate) || got.ReportedCost.Source != "" {
			t.Fatalf("occurrence %d = %+v", i, got)
		}
		for _, forbidden := range []string{"duplicate-id-private", "deployment-private", "source-private", "private prompt"} {
			if bytes.Contains(row.Accounting.PayloadJSON, []byte(forbidden)) {
				t.Fatalf("private metadata retained: %s", forbidden)
			}
		}
	}
	attempts, err := store.ListHeadlessAttempts(t.Context(), string(first.ID))
	if err != nil || len(attempts) != 2 || attempts[0].DecisionDone || !attempts[1].DecisionDone {
		t.Fatalf("summaries = %+v: %v", attempts, err)
	}
	// The legacy report remains scoped to the last Run, not all three occurrences.
	if report := engine.Report(first); report.Timing.ProviderAttempts != 1 || len(report.ProviderObservations) != 1 || report.TotalUsage.TotalTokens != fixture.Usage.TotalTokens {
		t.Fatal("legacy last-Run accounting changed")
	}
	live.SetVerifyLoop("", 0)
	second := live.RunHeadless(t.Context(), "another run", 1)
	if second.Err != nil || second.ID == first.ID {
		t.Fatalf("second drive: %+v", second)
	}
	secondRows := headlessRows(t, store, string(second.ID))
	if len(secondRows) != 1 || secondRows[0].Accounting.PursueAttempt != 1 || secondRows[0].Accounting.ProviderOrdinal != 1 || len(headlessRows(t, store, string(first.ID))) != 3 {
		t.Fatal("independent run identity/reset changed")
	}
	_, roots, children, err := live.RecordedHistoryWithAccounting("unrelated-session")
	if err != nil || len(roots) != 0 || len(children) != 0 {
		t.Fatal("headless accounting contaminated session accounting", err)
	}
}

func TestHeadlessAccountingCapturesBeforeMutatingBlockingSinkAndGoal(t *testing.T) {
	fixture := recordedAccountingFixture()
	var root string
	started, providerRelease := make(chan struct{}), make(chan struct{})
	unblockProvider := sync.OnceFunc(func() { close(providerRelease) })
	sink := &blockingAccountingSink{entered: make(chan struct{}), release: make(chan struct{})}
	unblockSink := sync.OnceFunc(func() { close(sink.release) })
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			close(started)
			select {
			case <-providerRelease:
			case <-ctx.Done():
				return
			}
			if err := os.WriteFile(filepath.Join(root, "agent-edit"), nil, 0o600); err != nil {
				t.Error(err)
			}
			yield(llm.CompletionChunk{Content: "done", UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage, Execution: &llm.ExecutionObservation{ReportedCost: *fixture.ReportedCost}}, nil)
		}
	})
	live, store, workspace := headlessAccountingLive(t, provider, engine.WithLiveSink(sink))
	root = workspace
	if out, err := exec.CommandContext(t.Context(), "git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "frozen route", Model: "frozen model"}, RateCard: fixture.RateCard})
	live.SetVerifyLoop("touch goal-evaluated", 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan runner.TaskResult, 1)
	go func() { done <- live.RunHeadless(ctx, "capture", 1) }()
	joined := false
	defer func() {
		cancel()
		unblockProvider()
		unblockSink()
		if !joined {
			<-done
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	next := fixture.RateCard.Clone()
	next.Reference.Revision, next.FreshInput.PerMillionTokens = "future", 999
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "future", Model: "future"}, RateCard: &next})
	unblockProvider()
	select {
	case <-sink.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := os.Stat(filepath.Join(root, "goal-evaluated")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Goal evaluated before capture/downstream returned", err)
	}
	unblockSink()
	result := <-done
	joined = true
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	rows := headlessRows(t, store, string(result.ID))
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	got := decodeRecordedHeadlessAccounting(t, rows[0].Accounting.PayloadJSON)
	if got.Provider != "frozen route" || got.Model != "frozen model" || !reflect.DeepEqual(got.RateCard, fixture.RateCard) || !reflect.DeepEqual(got.Usage, fixture.Usage) || !reflect.DeepEqual(got.PricingUsage, fixture.PricingUsage) || !reflect.DeepEqual(got.ConfiguredEstimate, fixture.ConfiguredEstimate) || got.ReportedCost.Amount != fixture.ReportedCost.Amount {
		t.Fatalf("downstream mutation/live target changed capture: %+v", got)
	}
}

func TestHeadlessAccountingExcludesFallbackChildren(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[taskscope.ID]int)
	childCalls := 0
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		id, depth := taskscope.IDFrom(ctx), taskscope.DepthFrom(ctx)
		mu.Lock()
		calls[id]++
		call := calls[id]
		if depth > 0 {
			childCalls++
		}
		mu.Unlock()
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{TotalTokens: 10 + depth}}, nil)
			if depth == 0 && call == 1 {
				yield(childSpawnChunk(`{"prompt":"read only child","mode":"explore"}`), nil)
			} else {
				yield(llm.CompletionChunk{Content: "Read-only evidence is complete.", FinishReason: llm.FinishReasons.STOP}, nil)
			}
		}
	})
	live, store, _ := headlessAccountingLive(t, provider, engine.WithReadOnlyTasks())
	live.SetLimits(1024, 8, 4, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := live.RunHeadless(ctx, "delegate read only", 8)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	mu.Lock()
	children, roots := childCalls, calls[result.ID]
	mu.Unlock()
	rows := headlessRows(t, store, string(result.ID))
	if children == 0 || len(rows) != roots {
		t.Fatalf("children=%d roots=%d accounting=%d", children, roots, len(rows))
	}
	for _, row := range rows {
		got := decodeRecordedHeadlessAccounting(t, row.Accounting.PayloadJSON)
		if got.Usage.TotalTokens != 10 || got.PursueAttempt != 1 {
			t.Fatal("child usage included in root occurrence")
		}
	}
}

func TestHeadlessAccountingAbsentAndPartialFailureEvidence(t *testing.T) {
	for _, reported := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "partial failure"}[reported], func(t *testing.T) {
			providerErr := errors.New("private provider diagnostic")
			provider := recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					yield(llm.CompletionChunk{UsageReported: reported, Usage: llm.Usage{PromptTokens: 7, TotalTokens: 7}}, nil)
					yield(llm.CompletionChunk{}, providerErr)
				}
			})
			live, store, _ := headlessAccountingLive(t, provider)
			result := live.RunHeadless(t.Context(), "private prompt", 1)
			if !errors.Is(result.Err, providerErr) || result.Reason != runner.TerminalError {
				t.Fatalf("failure verdict: %+v", result)
			}
			rows := headlessRows(t, store, string(result.ID))
			if len(rows) != 1 {
				t.Fatalf("settled failure rows=%d", len(rows))
			}
			got := decodeRecordedHeadlessAccounting(t, rows[0].Accounting.PayloadJSON)
			if (got.Usage != nil) != reported || got.PricingUsage != nil || got.ConfiguredEstimate != nil || got.ReportedCost != nil {
				t.Fatalf("fabricated evidence: %+v", got)
			}
			if reported && got.Usage.PromptTokens != 7 {
				t.Fatal("lost partial failure usage")
			}
			if bytes.Contains(rows[0].Accounting.PayloadJSON, []byte("private")) {
				t.Fatal("prompt/error entered accounting")
			}
		})
	}
}

func TestHeadlessAccountingExcludesActualJudgeAndPlanner(t *testing.T) {
	settings := newJudgeTestSettings(t)
	for key, value := range map[string]string{
		prefs.KeyJudgeProvider:   prefs.ActiveModelTarget,
		prefs.KeyCompactProvider: prefs.ActiveModelTarget,
		prefs.KeyCompactionMode:  "manual", prefs.KeyProgrammaticTools: "off",
		prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultImplementModel:    "saved-model",
	} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	root := settings.WorkspaceRoot()
	dir := filepath.Join(root, ".zarlcode", "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture-agent.md"), []byte("---\nname: fixture-agent\ndescription: Accounting helper fixture\nmode: implement\n---\nInspect fixture evidence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &auxiliaryRunProvider{recurse: true}
	live := requestControlRunner(t, p, root, engine.WithSettings(settings))
	live.SetLimits(512, 12, 12, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result := live.RunHeadlessWithObservations(ctx, "Exercise actual helpers with durable accounting.", 12)
	if result.Task.Err != nil || result.Auxiliary.Judge == nil || result.Auxiliary.SpawnPlanner == nil {
		t.Fatalf("helpers not exercised: %+v", result)
	}
	rows := headlessRows(t, settings.Store, string(result.Task.ID))
	p.mu.Lock()
	rootCalls := p.calls[result.Task.ID]
	p.mu.Unlock()
	if len(rows) != rootCalls {
		t.Fatalf("root calls=%d accounting=%d", rootCalls, len(rows))
	}
	for _, row := range rows {
		got := decodeRecordedHeadlessAccounting(t, row.Accounting.PayloadJSON)
		if got.Usage.PromptTokens != 2 || bytes.Contains(row.Accounting.PayloadJSON, []byte("SIDECAR_ONLY_CANARY")) {
			t.Fatal("helper usage/metadata contaminated main occurrence")
		}
	}
}
