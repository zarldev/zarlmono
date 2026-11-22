package engine_test

import (
	"bytes"
	"context"
	"os"
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

func headlessChildRows(t *testing.T, store *db.Store, runID string) []db.HeadlessChildProviderAccountingRecord {
	t.Helper()
	rows, err := store.ListHeadlessChildProviderAccounting(t.Context(), runID, 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func headlessChildLive(t *testing.T, provider llm.Provider, extra ...options.Option[engine.LiveRunner]) (*engine.LiveRunner, *db.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, root)
	for key, value := range map[string]string{
		prefs.KeySpawnDefaultExploreProvider:   prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultVerifyProvider:    prefs.ActiveModelTarget,
		prefs.KeySpawnDefaultImplementProvider: prefs.ActiveModelTarget,
		prefs.KeySpawnFallback:                 "parent",
		prefs.KeyDecomposeJudge:                "off",
	} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	opts := append([]options.Option[engine.LiveRunner]{engine.WithSettings(settings), engine.WithReadOnlyTasks()}, extra...)
	live := requestControlRunner(t, provider, root, opts...)
	return live, store, root
}

func TestHeadlessChildCaptureFallbackDefaultAndNamed(t *testing.T) {
	for _, route := range []string{"fallback", "default", "named"} {
		t.Run(route, func(t *testing.T) {
			fixture := recordedAccountingFixture()
			var mu sync.Mutex
			calls := make(map[taskscope.ID]int)
			provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				id, depth := taskscope.IDFrom(ctx), taskscope.DepthFrom(ctx)
				mu.Lock()
				calls[id]++
				attempt := calls[id]
				mu.Unlock()
				return func(yield func(llm.CompletionChunk, error) bool) {
					chunk := llm.CompletionChunk{UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage,
						Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true, Amount: 17, Currency: "JPY", Source: "private-source-canary"}}}
					if !yield(chunk, nil) {
						return
					}
					if depth == 0 && attempt == 1 {
						args := `{"agent":"missing-agent","prompt":"private child assignment","mode":"explore"}`
						switch route {
						case "named":
							args = `{"agent":"headless-child","prompt":"private child assignment","mode":"explore"}`
						case "default":
							args = `{"prompt":"private child assignment","mode":"explore"}`
						}
						yield(childSpawnChunk(args), nil)
						return
					}
					yield(llm.CompletionChunk{Content: "Read-only evidence complete.", FinishReason: llm.FinishReasons.STOP}, nil)
				}
			})
			live, store, workspace := headlessChildLive(t, provider)
			if route == "named" {
				dir := filepath.Join(workspace, ".zarlcode", "agents")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "headless-child.md"), []byte("---\nname: headless-child\ndescription: Headless capture fixture\nmode: explore\n---\nRead only.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "actual-route", Model: "actual-model"}, RateCard: fixture.RateCard})
			live.SetLimits(1024, 8, 4, 2)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := live.RunHeadless(ctx, "private root prompt", 8)
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			rows := headlessChildRows(t, store, string(result.ID))
			if len(rows) != 1 {
				t.Fatalf("child rows=%d", len(rows))
			}
			got, err := engine.DecodeRecordedHeadlessChildProviderAccounting(rows[0].Accounting.PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			if got.RunID != string(result.ID) || got.ChildTaskID == string(result.ID) || got.ChildTaskID != rows[0].Accounting.ChildTaskID || got.Attempt != 1 || !reflect.DeepEqual(got.Usage, fixture.Usage) || got.ReportedCost.Source != "" {
				t.Fatalf("child identity/evidence: %+v", got)
			}
			if got.Provider != "actual-route" || got.Model != "actual-model" || !reflect.DeepEqual(got.RateCard, fixture.RateCard) || !reflect.DeepEqual(got.ConfiguredEstimate, fixture.ConfiguredEstimate) {
				t.Fatalf("%s actual target: %+v", route, got)
			}
			for _, secret := range []string{"private-source-canary", "private child assignment", "private root prompt"} {
				if bytes.Contains(rows[0].Accounting.PayloadJSON, []byte(secret)) {
					t.Fatalf("child payload leaked %q", secret)
				}
			}
			if roots := headlessRows(t, store, string(result.ID)); len(roots) < 2 {
				t.Fatalf("root occurrences=%d", len(roots))
			}
		})
	}
}

func TestHeadlessChildNoStreamNoRow(t *testing.T) {
	provider := recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "done", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	live, store, _ := headlessAccountingLive(t, provider)
	result := live.RunHeadless(t.Context(), "no child", 1)
	if result.Reason != runner.TerminalCompleted || len(headlessChildRows(t, store, string(result.ID))) != 0 {
		t.Fatalf("unexpected child rows: %+v", result)
	}
}

func TestHeadlessChildWithoutStoreKeepsRawDelivery(t *testing.T) {
	var mu sync.Mutex
	rootCalls := 0
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		depth := taskscope.DepthFrom(ctx)
		mu.Lock()
		if depth == 0 {
			rootCalls++
		}
		call := rootCalls
		mu.Unlock()
		return func(yield func(llm.CompletionChunk, error) bool) {
			if depth == 0 && call == 1 {
				yield(childSpawnChunk(`{"agent":"missing-agent","prompt":"raw child","mode":"explore"}`), nil)
				return
			}
			yield(llm.CompletionChunk{Content: "Read-only evidence complete.", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	sink := &childRateCardSink{}
	live := requestControlRunner(t, provider, t.TempDir(), engine.WithReadOnlyTasks(), engine.WithLiveSink(sink))
	live.SetLimits(1024, 8, 4, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result := live.RunHeadless(ctx, "raw path", 8)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	child := false
	for _, event := range sink.events {
		child = child || event.Depth > 0
	}
	if !child {
		t.Fatal("child did not settle through downstream without a store")
	}
}
