package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func manualHistory() []llm.Message {
	var history []llm.Message
	for range 20 {
		history = append(history, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("old prompt ", 20)}, llm.Message{Role: llm.RoleAssistant, Content: "old answer"})
	}
	return history
}

func TestCompactRecordedSessionCaptureAndACK(t *testing.T) {
	for _, kind := range []string{"summary", "executive", "handover", "tiered", "structural"} {
		t.Run(kind, func(t *testing.T) {
			settings := recordedAuxiliarySettings(t, map[string]string{prefs.KeyCompactEngine: kind, prefs.KeyCompactionMode: "manual"})
			var calls int
			provider := recordedAccountingProvider(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				calls++
				return func(yield func(llm.CompletionChunk, error) bool) {
					e := auxiliaryEvidence()
					yield(llm.CompletionChunk{Content: "short offline summary", UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil)
					e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 99999
				}
			})
			live := recordedAuxiliaryLive(t, settings, provider)
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
			if err != nil {
				t.Fatal(err)
			}
			if len(batch.Roots) != 0 || len(batch.Children) != 0 || len(batch.Auxiliaries) != 0 {
				t.Fatal("fabricated turn receipt")
			}
			if kind == "tiered" || kind == "structural" {
				if len(batch.Manual) != 0 || calls != 0 {
					t.Fatal("model-free compaction charged")
				}
				return
			}
			if len(batch.Manual) != calls || calls == 0 {
				t.Fatalf("rows=%d calls=%d", len(batch.Manual), calls)
			}
			purpose := map[string]engine.AuxiliaryAccountingPurpose{"summary": engine.AuxiliaryAccountingPurposes.PURPOSESUMMARY, "executive": engine.AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE, "handover": engine.AuxiliaryAccountingPurposes.PURPOSEHANDOVER}[kind]
			for _, row := range batch.Manual {
				record, err := engine.DecodeRecordedSessionManualAccounting(row.PayloadJSON)
				if err != nil {
					t.Fatal(err)
				}
				if record.SessionID != "session" || record.InvocationID != row.InvocationID || record.Purpose != purpose || record.StreamOutcome != inference.StreamOutcomes.COMPLETED || record.Provider != "actual-route" || record.Model != "actual-model" || record.RateCard == nil || record.ConfiguredEstimate == nil || record.Usage == nil {
					t.Fatalf("bad manual capture: %+v", record)
				}
				if strings.Contains(string(row.PayloadJSON), "99999") {
					t.Fatal("borrowed sidecar changed capture")
				}
			}
			original := batch.Manual[0].Clone()
			batch.Manual[0].PayloadJSON[0] = '!'
			_, again, err := live.RecordedHistoryWithAccountingBatch("session")
			if err != nil || !reflect.DeepEqual(again.Manual[0], original) {
				t.Fatal("borrowed snapshot", err)
			}
			for _, changed := range []func(*db.SessionManualProviderAccounting){func(r *db.SessionManualProviderAccounting) { r.Version++ }, func(r *db.SessionManualProviderAccounting) { r.InvocationID = "different" }, func(r *db.SessionManualProviderAccounting) { r.PayloadJSON[0] = '!' }} {
				stale := original.Clone()
				changed(&stale)
				live.AcknowledgeRecordedHistoryWithAccountingBatch("session", nil, db.SessionAccountingBatch{Manual: []db.SessionManualProviderAccounting{stale}})
				_, pending, err := live.RecordedHistoryWithAccountingBatch("session")
				if err != nil || len(pending.Manual) != calls {
					t.Fatal("stale ACK removed capture", err)
				}
			}
			live.AcknowledgeRecordedHistoryWithAccounting("session", nil, nil, nil)
			_, pending, err := live.RecordedHistoryWithAccountingBatch("session")
			if err != nil || len(pending.Manual) != calls {
				t.Fatal("legacy ACK removed capture", err)
			}
			live.AcknowledgeRecordedHistoryWithAccountingBatch("session", nil, again)
			_, pending, err = live.RecordedHistoryWithAccountingBatch("session")
			if err != nil || len(pending.Manual) != 0 {
				t.Fatal("exact ACK retained capture", err)
			}
			if _, err := live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
				t.Fatalf("reservation released: %v", err)
			}
		})
	}
}
