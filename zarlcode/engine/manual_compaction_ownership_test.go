package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCompactRecordedSessionReservationAndFailureEvidence(t *testing.T) {
	for _, failure := range []string{"stream error", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: "summary", prefs.KeyCompactionMode: "manual"})
			var cancel context.CancelFunc
			provider := recordedAccountingProvider(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					yield(llm.CompletionChunk{Content: "partial", UsageReported: true, Usage: llm.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}}, nil)
					if failure == "cancel" {
						cancel()
						yield(llm.CompletionChunk{}, context.Canceled)
						return
					}
					yield(llm.CompletionChunk{}, errors.New("offline interruption"))
				}
			})
			live := recordedAuxiliaryLive(t, settings, provider)
			live.RestoreContext(manualHistory())
			ctx, end := context.WithCancel(t.Context())
			cancel = end
			defer end()
			ordinary, err := live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ordinary.CompactRecordedSession(ctx, "session"); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("nonturn reservation: %v", err)
			}
			ordinary.Release()
			reservation, err := live.ReserveTurn()
			if err != nil {
				t.Fatal(err)
			}
			_, compactErr := reservation.CompactRecordedSession(ctx, "session")
			if compactErr == nil {
				t.Fatal("stream error lost")
			}
			if failure == "cancel" && !errors.Is(compactErr, context.Canceled) {
				t.Fatalf("cancellation cause lost: %v", compactErr)
			}
			_, batch, err := live.RecordedHistoryWithAccountingBatch("session")
			if err != nil || len(batch.Manual) != 1 {
				t.Fatalf("incurred evidence lost: %v %+v", err, batch)
			}
			row, err := engine.DecodeRecordedSessionManualAccounting(batch.Manual[0].PayloadJSON)
			if err != nil || row.Usage == nil || row.Usage.PromptTokens != 7 {
				t.Fatalf("partial usage: %+v %v", row, err)
			}
			if _, err := live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("released before save: %v", err)
			}
			reservation.Release()
			if _, err := reservation.CompactRecordedSession(t.Context(), "session"); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("inactive reservation: %v", err)
			}
			if _, id := live.QueueAppend("queued"); id == 0 {
				t.Fatal("queue not admitted")
			}
			if _, err := live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("queued input admitted: %v", err)
			}
		})
	}
}

func TestCompactRecordedSessionNoProviderAndCaptureError(t *testing.T) {
	settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: "summary", prefs.KeyCompactionMode: "manual"})
	calls := 0
	provider := recordedAccountingProvider(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		calls++
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "offline summary", UsageReported: true, Usage: llm.Usage{PromptTokens: 2, TotalTokens: 2}}, nil)
		}
	})
	live := recordedAuxiliaryLive(t, settings, provider)
	reservation, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reservation.CompactRecordedSession(t.Context(), "session"); err != nil {
		t.Fatal(err)
	}
	_, batch, err := live.RecordedHistoryWithAccountingBatch("session")
	if err != nil || len(batch.Manual) != 0 || calls != 0 {
		t.Fatalf("empty context invoked provider: %v %+v", err, batch)
	}
	reservation.Release()
	live.RestoreContext(manualHistory())
	reservation, err = live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reservation.CompactRecordedSession(t.Context(), ""); !errors.Is(err, engine.ErrInvalidRecordedSessionManualAccounting) {
		t.Fatalf("capture failure not returned: %v", err)
	}
	_, _, err = live.RecordedHistoryWithAccountingBatch("")
	if !errors.Is(err, engine.ErrInvalidRecordedSessionManualAccounting) {
		t.Fatalf("capture failure not surfaced: %v", err)
	}
	reservation.Release()
}

func TestCompactRecordedSessionModelOverrideAndFallback(t *testing.T) {
	for _, tc := range []struct{ name, provider, model, diagnosticModel string }{
		{"model-only", prefs.ActiveModelTarget, "manual-model", "manual-model"},
		{"failed target", "unbuildable-fixture", "rejected-model", "actual-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: "summary", prefs.KeyCompactionMode: "manual", prefs.KeyCompactProvider: tc.provider, prefs.KeyCompactModel: tc.model})
			provider := recordedAccountingProvider(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					e := auxiliaryEvidence()
					yield(llm.CompletionChunk{Content: "offline summary", UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage}, nil)
				}
			})
			sink := &recordedCompactSink{}
			live := recordedAuxiliaryLive(t, settings, provider, engine.WithLiveSink(sink))
			live.RestoreContext(manualHistory())
			reservation, err := live.ReserveTurn()
			if err != nil {
				t.Fatal(err)
			}
			defer reservation.Release()
			if _, err := reservation.CompactRecordedSession(t.Context(), "session"); err != nil {
				t.Fatal(err)
			}
			_, batch, err := live.RecordedHistoryWithAccountingBatch("session")
			if err != nil || len(batch.Manual) != 1 {
				t.Fatalf("capture: %v %+v", err, batch)
			}
			record, err := engine.DecodeRecordedSessionManualAccounting(batch.Manual[0].PayloadJSON)
			if err != nil || record.Model != "actual-model" || record.Provider != "actual-route" || record.RateCard == nil || record.ConfiguredEstimate == nil {
				t.Fatalf("actual target: %+v %v", record, err)
			}
			if len(sink.labels) != 1 || sink.labels[0] != tc.diagnosticModel {
				t.Fatalf("diagnostic labels: %v", sink.labels)
			}
		})
	}
}

func TestCompactRecordedSessionInvalidNumericCapturePoisonsSnapshot(t *testing.T) {
	settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: "summary", prefs.KeyCompactionMode: "manual"})
	streamErr := errors.New("offline interruption")
	provider := recordedAccountingProvider(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "private-unsafe-canary", PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true, Tokens: -1}}}}, streamErr)
		}
	})
	live := recordedAuxiliaryLive(t, settings, provider)
	live.RestoreContext(manualHistory())
	reservation, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	_, err = reservation.CompactRecordedSession(t.Context(), "session")
	if !errors.Is(err, engine.ErrInvalidRecordedSessionManualAccounting) || !errors.Is(err, streamErr) || strings.Contains(err.Error(), "canary") {
		t.Fatalf("capture error: %v", err)
	}
	_, batch, snapshotErr := live.RecordedHistoryWithAccountingBatch("session")
	if !errors.Is(snapshotErr, engine.ErrInvalidRecordedSessionManualAccounting) || len(batch.Manual) != 0 || strings.Contains(snapshotErr.Error(), "canary") {
		t.Fatalf("unsafe or partial snapshot: %v %+v", snapshotErr, batch)
	}
}
