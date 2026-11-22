package engine_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func auxiliaryBatch(t *testing.T, live *engine.LiveRunner) ([]db.SessionHistoryBatch, db.SessionAccountingBatch) {
	t.Helper()
	history, batch, err := live.RecordedHistoryWithAccountingBatch("session")
	if err != nil {
		t.Fatal(err)
	}
	return history, batch
}

func TestRecordedAuxiliaryRecursiveCaptureAndExactACK(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "default siblings", true: "named recursive"}[named], func(t *testing.T) {
			store := recordedAccountingStore(t)
			p := &auxiliaryRunProvider{recurse: true, siblings: !named, named: named}
			var live *engine.LiveRunner
			live = newAuxiliaryRunner(t, p, func(_ context.Context, o engine.AuxiliaryObservation) {
				_, batch := auxiliaryBatch(t, live)
				if len(batch.Auxiliaries) == 0 {
					t.Error("semantic callback preceded durable capture")
				}
				mutateAuxiliary(&o)
			})
			card := recordedAccountingFixture().RateCard
			live.ApplyTarget(engine.TargetUpdate{Provider: p, Spec: engine.ProviderSpec{Model: "root-model"}, RateCard: card})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := live.RunRecordedTurn(ctx, "read-only descendants", nil, store, "session"); err != nil {
				t.Fatal(err)
			}
			history, first := auxiliaryBatch(t, live)
			wantJudge, wantPlanner := 5, 4
			if named {
				wantJudge, wantPlanner = 3, 1
			}
			if len(first.Auxiliaries) != wantJudge+wantPlanner {
				t.Fatalf("aux=%d want %d", len(first.Auxiliaries), wantJudge+wantPlanner)
			}
			counts := make(map[engine.AuxiliaryAccountingPurpose]int)
			ids := make(map[string]bool)
			owner := first.Roots[0].RequestBatchID
			selectedChild := false
			for _, child := range first.Children {
				record, err := engine.DecodeRecordedChildProviderAccounting(child.PayloadJSON)
				if err != nil {
					t.Fatal(err)
				}
				if record.Model == "default-model" || record.Model == "named-model" {
					selectedChild = true
				}
			}
			if !selectedChild {
				t.Fatal("fixture fell back instead of building configured child")
			}

			for _, row := range first.Auxiliaries {
				record, err := engine.DecodeRecordedSessionAuxiliaryAccounting(row.PayloadJSON)
				if err != nil {
					t.Fatal(err)
				}
				id, err := uuid.Parse(record.InvocationID)
				if err != nil || id.Version() != 4 || ids[row.InvocationID] {
					t.Fatal("nonfresh invocation", err)
				}
				ids[row.InvocationID] = true
				counts[record.Purpose]++
				if record.OwnerRequestBatchID != owner || record.Provider != "" || record.Model != "root-model" || record.StreamOutcome != inference.StreamOutcomes.COMPLETED || record.Usage.PromptTokens != 1000 || record.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 4 || record.RateCard == nil || record.ConfiguredEstimate == nil {
					t.Fatalf("incorrect frozen capture %+v", record)
				}
				for _, canary := range []string{"SIDECAR_ONLY_CANARY", "borrowed-mutated", "missing-auxiliary-file", "auxiliary descendant"} {
					if bytes.Contains(row.PayloadJSON, []byte(canary)) {
						t.Fatal("private evidence persisted")
					}
				}
			}
			if counts[engine.AuxiliaryAccountingPurposes.PURPOSEJUDGE] != wantJudge || counts[engine.AuxiliaryAccountingPurposes.PURPOSESPAWNPLANNER] != wantPlanner {
				t.Fatal(counts)
			}
			// Owned snapshots and exact owner/version/byte matching; legacy ACKs must
			// retain auxiliaries even when they release all their historical sets.
			for _, change := range []func(*db.SessionAuxiliaryProviderAccounting){
				func(row *db.SessionAuxiliaryProviderAccounting) { row.PayloadJSON[0] = '!' },
				func(row *db.SessionAuxiliaryProviderAccounting) { row.Version++ },
				func(row *db.SessionAuxiliaryProviderAccounting) { row.OwnerRequestBatchID = "other" },
				func(row *db.SessionAuxiliaryProviderAccounting) { row.InvocationID = uuid.NewString() },
			} {
				altered := first.Auxiliaries[0].Clone()
				change(&altered)
				live.AcknowledgeRecordedHistoryWithAccountingBatch("session", nil, db.SessionAccountingBatch{Auxiliaries: []db.SessionAuxiliaryProviderAccounting{altered}})
				_, pending := auxiliaryBatch(t, live)
				if !reflect.DeepEqual(pending.Auxiliaries, first.Auxiliaries) {
					t.Fatal("changed ACK released evidence")
				}
			}
			record, err := store.GetSession(t.Context(), "session")
			if err != nil {
				t.Fatal(err)
			}
			update := db.TranscriptUpdate{SessionID: "session", Workspace: "/workspace"}
			for range 2 {
				if err := store.CommitCompletedTurnWithAccountingBatch(t.Context(), record, update, first, history...); err != nil {
					t.Fatal(err)
				}
			}
			saved, err := store.ListSessionAuxiliaryProviderAccounting(t.Context(), "session", 0, 256)
			if err != nil || len(saved) != len(first.Auxiliaries) {
				t.Fatal("retry duplicated rows", err)
			}
			live.AcknowledgeRecordedHistoryWithAccounting("session", history, first.Roots, first.Children)
			_, pending := auxiliaryBatch(t, live)
			if !reflect.DeepEqual(first.Auxiliaries, pending.Auxiliaries) {
				t.Fatal("legacy ACK released auxiliary evidence")
			}
			live.ClearContext()
			p.recurse = false
			if err := live.RunRecordedTurn(ctx, "another identical helper", nil, store, "session"); err != nil {
				t.Fatal(err)
			}
			_, all := auxiliaryBatch(t, live)
			if len(all.Auxiliaries) != len(first.Auxiliaries)+1 {
				t.Fatalf("later aux=%d", len(all.Auxiliaries))
			}
			for range 2 {
				live.AcknowledgeRecordedHistoryWithAccountingBatch("session", history, first)
			}
			_, pending = auxiliaryBatch(t, live)
			if len(pending.Auxiliaries) != 1 || !reflect.DeepEqual(pending.Auxiliaries[0], all.Auxiliaries[len(all.Auxiliaries)-1]) {
				t.Fatal("stale ACK released later append")
			}
		})
	}
}

type auxiliaryOutcomeProvider struct {
	underlying auxiliaryRunProvider
	outcome    string
	cause      error
}

func (*auxiliaryOutcomeProvider) Name() string { return "aux-outcome" }
func (p *auxiliaryOutcomeProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	if req.ResponseFormat.Name != "decompose_verdict" {
		return p.underlying.Complete(ctx, req)
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		e := auxiliaryEvidence()
		yield(llm.CompletionChunk{Content: map[bool]string{true: "", false: "malformed private response canary"}[p.outcome == "empty"], UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil)
		switch p.outcome {
		case "failed":
			yield(llm.CompletionChunk{}, p.cause)
		case "cancelled":
			yield(llm.CompletionChunk{}, context.Canceled)
		}
	}
}

func TestRecordedAuxiliaryStreamOutcomesAndSafeErrors(t *testing.T) {
	for _, outcome := range []string{"malformed", "empty", "failed", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			store := recordedAccountingStore(t)
			p := &auxiliaryOutcomeProvider{outcome: outcome, cause: errors.New("https://private-error-canary.invalid/token")}
			live := newAuxiliaryRunner(t, p, func(context.Context, engine.AuxiliaryObservation) {})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			_ = live.RunRecordedTurn(ctx, "trigger repeated signature", nil, store, "session")
			_, batch := auxiliaryBatch(t, live)
			if len(batch.Auxiliaries) != 1 {
				t.Fatalf("aux=%d", len(batch.Auxiliaries))
			}
			got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(batch.Auxiliaries[0].PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			want := inference.StreamOutcomes.COMPLETED
			if outcome == "failed" {
				want = inference.StreamOutcomes.FAILED
			}
			if outcome == "cancelled" {
				want = inference.StreamOutcomes.CANCELLED
			}
			if got.StreamOutcome != want || got.Usage.PromptTokens != 1000 || got.ReportedCost == nil || got.ReportedCost.Source != "" {
				t.Fatalf("outcome %+v", got)
			}
			if bytes.Contains(batch.Auxiliaries[0].PayloadJSON, []byte("canary")) {
				t.Fatal("raw error/response retained")
			}
		})
	}
}

func TestRecordedAuxiliaryCapturedBeforeBlockedMutatingSemanticCallback(t *testing.T) {
	store := recordedAccountingStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	p := &auxiliaryRunProvider{}
	live := newAuxiliaryRunner(t, p, func(_ context.Context, o engine.AuxiliaryObservation) {
		mutateAuxiliary(&o)
		close(entered)
		<-release
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- live.RunRecordedTurn(ctx, "block only helper callback", nil, store, "session") }()
	defer func() { unblock(); <-done }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, batch := auxiliaryBatch(t, live)
	if len(batch.Auxiliaries) != 1 {
		t.Fatal("capture delayed until after callback")
	}
	got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(batch.Auxiliaries[0].PayloadJSON)
	if err != nil || got.Usage.PromptTokens != 1000 {
		t.Fatal("callback mutated evidence", err)
	}
}

func TestRecordedAuxiliaryLatestIndependentEvidencePresence(t *testing.T) {
	for _, presence := range []string{"absent", "zero", "latest"} {
		t.Run(presence, func(t *testing.T) {
			store := recordedAccountingStore(t)
			base := &auxiliaryRunProvider{}
			provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				if req.ResponseFormat.Name != "decompose_verdict" {
					return base.Complete(ctx, req)
				}
				return func(yield func(llm.CompletionChunk, error) bool) {
					if presence == "latest" {
						e := auxiliaryEvidence()
						if !yield(llm.CompletionChunk{UsageReported: true, Usage: *e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution}, nil) {
							return
						}
						e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999999
						e.Execution.ReportedCost.Amount = 999999
					}
					if presence != "absent" {
						if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}, Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}}, nil) {
							return
						}
					}
					// Nil final sidecars retain the independently latest non-nil evidence.
					yield(llm.CompletionChunk{Content: `{"action":"smaller_scope","rationale":"fixture"}`}, nil)
				}
			})
			live := newAuxiliaryRunner(t, provider, func(context.Context, engine.AuxiliaryObservation) {})
			_ = live.RunRecordedTurn(t.Context(), "latest evidence", nil, store, "session")
			_, batch := auxiliaryBatch(t, live)
			if len(batch.Auxiliaries) != 1 {
				t.Fatal("missing helper")
			}
			got, err := engine.DecodeRecordedSessionAuxiliaryAccounting(batch.Auxiliaries[0].PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			if got.RateCard != nil || got.ConfiguredEstimate != nil {
				t.Fatal("unbound helper invented price basis")
			}
			if presence == "absent" {
				if got.Usage != nil || got.PricingUsage != nil || got.ReportedCost != nil {
					t.Fatal("absence became zero")
				}
			} else if got.Usage == nil || got.Usage.TotalTokens != 0 || got.PricingUsage == nil || !got.PricingUsage.Input.Total.Known || got.PricingUsage.Input.Total.Tokens != 0 || len(got.PricingUsage.Input.WritesByLifetime) != 0 || got.ReportedCost == nil || !got.ReportedCost.Reported || got.ReportedCost.Amount != 0 {
				t.Fatalf("latest/zero not retained: %+v", got)
			}
		})
	}
}

func TestRecordedAuxiliarySettlesDuringWriterConnectionWait(t *testing.T) {
	store := recordedAccountingStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	entered, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	base := &auxiliaryRunProvider{}
	judges := 0
	provider := recordedAccountingProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		stream := base.Complete(ctx, req)
		if req.ResponseFormat.Name != "decompose_verdict" {
			return stream
		}
		return func(yield func(llm.CompletionChunk, error) bool) {
			judges++
			if judges == 2 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					yield(llm.CompletionChunk{}, context.Cause(ctx))
					return
				}
			}
			stream(yield)
		}
	})
	live := newAuxiliaryRunner(t, provider, func(context.Context, engine.AuxiliaryObservation) {
		if judges == 2 {
			close(settled)
		}
	})
	if err := live.RunRecordedTurn(ctx, "original helper", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	live.ClearContext()
	var workers sync.WaitGroup
	defer func() { cancel(); unblock(); workers.Wait() }()
	runDone := make(chan error, 1)
	workers.Go(func() { runDone <- live.RunRecordedTurn(ctx, "late helper", nil, store, "session") })
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	history, frozen := auxiliaryBatch(t, live)
	if len(history) == 0 || len(frozen.Roots) == 0 || len(frozen.Auxiliaries) != 1 {
		t.Fatal("missing original snapshot")
	}
	record, err := store.GetSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	waitCount := store.DB().Stats().WaitCount
	commitDone := make(chan error, 1)
	workers.Go(func() {
		commitDone <- store.CommitCompletedTurnWithAccountingBatch(ctx, record, db.TranscriptUpdate{SessionID: "session", Workspace: "/workspace"}, frozen, history...)
	})
	// WaitCount advances only once BeginTx queues for the sole writer connection,
	// after the batch's frozen input copy. This is NOT a pause inside SQLite COMMIT.
	for store.DB().Stats().WaitCount == waitCount {
		select {
		case err := <-commitDone:
			t.Fatalf("commit did not block: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			runtime.Gosched()
		}
	}
	unblock()
	select {
	case <-settled:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, duringWait := auxiliaryBatch(t, live)
	if len(duringWait.Auxiliaries) != 2 {
		t.Fatal("actual helper did not append while writer waited")
	}
	late := duringWait.Auxiliaries[1].Clone()
	select {
	case err := <-commitDone:
		t.Fatalf("commit escaped held connection: %v", err)
	default:
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-commitDone; err != nil {
		t.Fatal(err)
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	saved, err := store.ListSessionAuxiliaryProviderAccounting(ctx, "session", 0, 256)
	if err != nil || len(saved) != 1 || !reflect.DeepEqual(saved[0].Accounting, frozen.Auxiliaries[0]) {
		t.Fatal("commit included evidence outside frozen snapshot", err)
	}
	for range 2 {
		live.AcknowledgeRecordedHistoryWithAccountingBatch("session", history, frozen)
	}
	for _, change := range []func(*db.SessionAuxiliaryProviderAccounting){
		func(row *db.SessionAuxiliaryProviderAccounting) { row.PayloadJSON[0] = '!' },
		func(row *db.SessionAuxiliaryProviderAccounting) { row.Version++ },
		func(row *db.SessionAuxiliaryProviderAccounting) { row.OwnerRequestBatchID = "stale-owner" },
		func(row *db.SessionAuxiliaryProviderAccounting) {
			row.InvocationID = frozen.Auxiliaries[0].InvocationID
		},
	} {
		altered := late.Clone()
		change(&altered)
		live.AcknowledgeRecordedHistoryWithAccountingBatch("session", history, db.SessionAccountingBatch{Auxiliaries: []db.SessionAuxiliaryProviderAccounting{altered}})
		_, pending := auxiliaryBatch(t, live)
		if len(pending.Auxiliaries) != 1 || !reflect.DeepEqual(pending.Auxiliaries[0], late) {
			t.Fatal("old/changed/stale ACK released late bytes")
		}
	}
	remainingHistory, remaining := auxiliaryBatch(t, live)
	if err := store.CommitCompletedTurnWithAccountingBatch(ctx, record, db.TranscriptUpdate{SessionID: "session", Workspace: "/workspace"}, remaining, remainingHistory...); err != nil {
		t.Fatal(err)
	}
	live.AcknowledgeRecordedHistoryWithAccountingBatch("session", remainingHistory, remaining)
	pendingHistory, pending := auxiliaryBatch(t, live)
	if len(pendingHistory) != 0 || len(pending.Roots) != 0 || len(pending.Children) != 0 || len(pending.Auxiliaries) != 0 {
		t.Fatal("exact successful ACK did not clear pending batch")
	}
	var path string
	if err := store.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.ListSessionAuxiliaryProviderAccounting(ctx, "session", 0, 256)
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(rows[0].Accounting, frozen.Auxiliaries[0]) || !reflect.DeepEqual(rows[1].Accounting, late) {
		t.Fatal("reopen changed exact original/late evidence", err)
	}
}
