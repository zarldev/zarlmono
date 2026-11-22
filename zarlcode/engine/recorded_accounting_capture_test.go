package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type recordedAccountingProvider func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (p recordedAccountingProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return p(ctx, request)
}
func (recordedAccountingProvider) Name() string { return "openai" }

func recordedAccountingStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(t.Context(), t.TempDir()+"/accounting.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeControlSession(t, store, "session")
	return store
}

func recordedAccountingSnapshot(t *testing.T, live *engine.LiveRunner, session string) ([]db.SessionHistoryBatch, []db.SessionProviderAccounting) {
	t.Helper()
	history, rows, err := live.RecordedHistoryWithProviderAccounting(session)
	if err != nil {
		t.Fatal(err)
	}
	return history, rows
}

func TestRecordedAccountingRetriesRunResetAndExactAcknowledgment(t *testing.T) {
	store := recordedAccountingStore(t)
	fixture := recordedAccountingFixture()
	calls := 0
	var requests [][]byte
	provider := recordedAccountingProvider(func(_ context.Context, request llm.CompletionRequest) llm.CompletionStream {
		data, err := runner.MarshalHistoryRequest(request)
		if err != nil {
			t.Error(err)
		}
		requests = append(requests, data)
		calls++
		attempt := calls
		return func(yield func(llm.CompletionChunk, error) bool) {
			chunk := llm.CompletionChunk{UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage,
				Execution: &llm.ExecutionObservation{GatewayCallID: "same-untrusted-call-canary", GatewayDeployment: "deployment-canary", ReportedCost: llm.ReportedCost{Amount: 17, Currency: "JPY", Reported: true, Source: "source-canary"}}}
			// Repeated frames are replacement snapshots, not additional charges.
			for range 2 {
				if !yield(chunk, nil) {
					return
				}
			}
			if attempt == 1 {
				yield(llm.CompletionChunk{}, &llm.RateLimitError{Retryable: true, RetryAfter: time.Nanosecond})
				return
			}
			yield(llm.CompletionChunk{Content: "done", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	live := requestControlRunner(t, provider, t.TempDir())
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: " Configured Provider ", Model: " Team/Alias "}, RateCard: fixture.RateCard})
	if err := live.RunRecordedTurn(t.Context(), "same prompt", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	firstHistory, first := recordedAccountingSnapshot(t, live, "session")
	if len(first) != 2 || !bytes.Equal(requests[0], requests[1]) {
		t.Fatalf("retry pairing: rows=%d identical=%v", len(first), bytes.Equal(requests[0], requests[1]))
	}
	ids := make(map[string]bool)
	for i, row := range first {
		got := decodeRecordedAccounting(t, row.PayloadJSON)
		if ids[row.RequestBatchID] || got.Attempt != i+1 || got.Provider != " Configured Provider " || got.Model != " Team/Alias " || got.Usage.TotalTokens != fixture.Usage.TotalTokens || got.ReportedCost.Amount != 17 || got.ReportedCost.Source != "" {
			t.Fatalf("incorrect settlement: %+v", got)
		}
		ids[row.RequestBatchID] = true
		estimate := fixture.ConfiguredEstimate
		if !reflect.DeepEqual(got.ConfiguredEstimate, estimate) {
			t.Fatal("configured estimate changed")
		}
		for _, forbidden := range []string{"same-untrusted-call-canary", "deployment-canary", "source-canary"} {
			if bytes.Contains(row.PayloadJSON, []byte(forbidden)) {
				t.Fatal("unallowlisted execution persisted")
			}
		}
		if _, err := engine.ReadRecordedProviderAccounting(t.Context(), store, "session", row.RequestBatchID); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("settlement callback wrote accounting: %v", err)
		}
	}
	// Snapshot and acknowledgement inputs may not alias the pending owner.
	altered := first[0].Clone()
	altered.PayloadJSON[0] = '!'
	live.AcknowledgeRecordedProviderAccounting("session", []db.SessionProviderAccounting{altered})
	_, pending := recordedAccountingSnapshot(t, live, "session")
	if !reflect.DeepEqual(pending, first) {
		t.Fatal("changed bytes acknowledged pending evidence")
	}
	record, err := store.GetSession(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	update := db.TranscriptUpdate{SessionID: "session", Workspace: "/workspace"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.CommitCompletedTurnWithProviderAccounting(ctx, record, update, first, firstHistory...); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	_, pending = recordedAccountingSnapshot(t, live, "session")
	if !reflect.DeepEqual(pending, first) {
		t.Fatal("failed save lost exact bytes")
	}
	for range 2 { // ambiguous successful write may be retried without an ACK
		if err := store.CommitCompletedTurnWithProviderAccounting(t.Context(), record, update, first, firstHistory...); err != nil {
			t.Fatal(err)
		}
	}
	// Another Run resets attempt but receives a fresh receipt; use reserved dispatch.
	reservation, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	if err := live.RunRecordedTurn(t.Context(), "refused runtime", nil, store, "session"); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("runtime admission: %v", err)
	}
	if err := reservation.RunRecordedTurn(t.Context(), "same prompt", nil, store, "session"); err != nil {
		reservation.Release()
		t.Fatal(err)
	}
	reservation.Release()
	_, all := recordedAccountingSnapshot(t, live, "session")
	if len(all) != 3 {
		t.Fatalf("all rows=%d", len(all))
	}
	last := decodeRecordedAccounting(t, all[2].PayloadJSON)
	if last.Attempt != 1 || ids[last.RequestBatchID] {
		t.Fatal("per-Run attempt reset collided")
	}
	for range 2 {
		live.AcknowledgeRecordedHistory("session", firstHistory)
		live.AcknowledgeRecordedProviderAccounting("session", first)
	}
	history, pending := recordedAccountingSnapshot(t, live, "session")
	if len(pending) != 1 || !bytes.Equal(pending[0].PayloadJSON, all[2].PayloadJSON) {
		t.Fatal("stale ACK dropped later settlement")
	}
	if err := store.CommitCompletedTurnWithProviderAccounting(t.Context(), record, update, pending, history...); err != nil {
		t.Fatal(err)
	}
	durable, err := store.ListSessionProviderAccounting(t.Context(), "session", 0, 256)
	if err != nil || len(durable) != 3 {
		t.Fatalf("durable rows=%d: %v", len(durable), err)
	}
	read, err := engine.ReadRecordedProviderAccounting(t.Context(), store, "session", first[0].RequestBatchID)
	if err != nil || !reflect.DeepEqual(read, decodeRecordedAccounting(t, first[0].PayloadJSON)) {
		t.Fatal("read changed frozen accounting", err)
	}
	live.AcknowledgeRecordedProviderAccounting("session", pending)
	if err := live.RunTurn(t.Context(), "not recorded"); err != nil {
		t.Fatal(err)
	}
	if got := live.RunHeadless(t.Context(), "headless", 1); got.Err != nil {
		t.Fatal(got.Err)
	}
	_, excluded := recordedAccountingSnapshot(t, live, "session")
	_, fork := recordedAccountingSnapshot(t, live, "fork")
	if len(excluded) != 0 || len(fork) != 0 {
		t.Fatal("non-recorded/headless/fork copied accounting")
	}
}

type blockingAccountingSink struct {
	runner.NopSink
	entered chan struct{}
	release chan struct{}
}

func (*blockingAccountingSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*blockingAccountingSink) PlanUpdated(string, code.Plan)    {}
func (s *blockingAccountingSink) OnProviderAttemptSettled(ctx context.Context, event runner.ProviderAttemptSettled) {
	event.Usage.TotalTokens = 999
	event.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
	event.RateCard.Reference.Revision = "sink mutation"
	event.RateCard.CacheWrites[0].Rate.PerMillionTokens = 999
	event.Execution.ReportedCost.Amount = 999
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
}

func TestRecordedAccountingFreezesTargetAndEvidenceBeforeBlockingSink(t *testing.T) {
	store := recordedAccountingStore(t)
	fixture := recordedAccountingFixture()
	started, releaseProvider := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(releaseProvider) })
	sink := &blockingAccountingSink{entered: make(chan struct{}), release: make(chan struct{})}
	releaseSink := sync.OnceFunc(func() { close(sink.release) })
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			close(started)
			select {
			case <-releaseProvider:
			case <-ctx.Done():
				return
			}
			yield(llm.CompletionChunk{Content: "done", UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage, Execution: &llm.ExecutionObservation{ReportedCost: *fixture.ReportedCost}}, nil)
		}
	})
	live := requestControlRunner(t, provider, t.TempDir(), engine.WithLiveSink(sink))
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "frozen provider", Model: "frozen model"}, RateCard: fixture.RateCard})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	go func() { done <- live.RunRecordedTurn(ctx, "go", nil, store, "session") }()
	joined := false
	defer func() {
		cancel()
		unblock()
		releaseSink()
		if !joined {
			<-done
		}
	}()
	select {
	case <-started:
	case err := <-done:
		joined = true
		t.Fatalf("run ended before provider: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	next := fixture.RateCard.Clone()
	next.Reference.Revision = "future revision"
	next.FreshInput.PerMillionTokens = 999
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "future provider", Model: "future model"}, RateCard: &next})
	unblock()
	select {
	case <-sink.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, rows := recordedAccountingSnapshot(t, live, "session")
	if len(rows) != 1 {
		t.Fatal("accounting blocked behind forwarded callback")
	}
	got := decodeRecordedAccounting(t, rows[0].PayloadJSON)
	if got.Provider != "frozen provider" || got.Model != "frozen model" || !reflect.DeepEqual(got.RateCard, fixture.RateCard) || !reflect.DeepEqual(got.PricingUsage, fixture.PricingUsage) || !reflect.DeepEqual(got.ConfiguredEstimate, fixture.ConfiguredEstimate) || got.Usage.TotalTokens != fixture.Usage.TotalTokens || got.ReportedCost.Amount != 17 {
		t.Fatal("live target or mutating sink changed captured evidence")
	}
	rows[0].PayloadJSON[0] = '!'
	_, again := recordedAccountingSnapshot(t, live, "session")
	if !bytes.Equal(again[0].PayloadJSON, encodeRecordedAccounting(t, got)) {
		t.Fatal("snapshot bytes alias pending envelope")
	}
	releaseSink()
	err := <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordedAccountingAdmissionRefusalHasNoSettlement(t *testing.T) {
	store := recordedAccountingStore(t)
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	root := t.TempDir()
	first := recordedSwarmRuntime(t, root, recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
			yield(llm.CompletionChunk{Content: "done"}, nil)
		}
	}))
	denied := recordedSwarmRuntime(t, root, recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		t.Error("refused invocation reached provider")
		return nil
	}))
	limits := swarm.DefaultLimits()
	limits.Workers = 2
	limits.Invocations = 1
	goal := engine.NewSwarm("record refusal", swarm.WithLimits(limits))
	if _, err := goal.AddRecorded("first", first, "go", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		return a.RunRecorded(ctx, store, "session").Err
	}); err != nil {
		t.Fatal(err)
	}
	refused := make(chan error, 1)
	if _, err := goal.AddRecorded("denied", denied, "go", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		select {
		case <-started:
		case <-ctx.Done():
			return ctx.Err()
		}
		result := a.RunRecorded(ctx, store, "session")
		refused <- result.Err
		unblock()
		return result.Err
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() { done <- goal.Run(ctx) }()
	defer func() { unblock(); _ = goal.Close(context.WithoutCancel(ctx)); <-done }()
	select {
	case err := <-refused:
		if !errors.Is(err, inference.ErrBudget) {
			t.Fatalf("refusal=%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	history, rows := recordedAccountingSnapshot(t, denied, "session")
	prepared := 0
	for _, batch := range history {
		if len(batch.RequestJSON) > 0 {
			prepared++
			if _, err := engine.ReadRecordedProviderAccounting(t.Context(), store, "session", batch.ID); !errors.Is(err, db.ErrNotFound) {
				t.Fatal("prepared refusal fabricated a row", err)
			}
		}
	}
	if prepared != 1 || len(rows) != 0 {
		t.Fatalf("prepared=%d settlements=%d", prepared, len(rows))
	}
}

func TestRecordedAccountingUsesPreparedDefaultAlias(t *testing.T) {
	store := recordedAccountingStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "claude/gpt-Gemini Production Alias" {
			t.Errorf("dispatched model=%q", request.Model)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Litellm-Response-Cost", "0")
		_, _ = io.WriteString(w, "data: "+`{"id":"untrusted-call-canary","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	settings := preparedGatewaySettings(server.URL)
	prepared, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err = engine.BindTargetRateCard(prepared, targetCardConfig(t, engine.RateCardTarget{Provider: prepared.Spec.Name, Model: llm.ModelID(prepared.Spec.Model), Protocol: prepared.Facts.SelectedProtocol}, "frozen-v1"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(prepared.Provider, ws, prepared.Spec.Model, engine.WithReadOnlyTasks())
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	live.ApplyTarget(prepared)
	if err := live.RunRecordedTurn(t.Context(), "Report.", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	_, rows := recordedAccountingSnapshot(t, live, "session")
	if len(rows) != 1 {
		t.Fatalf("default-alias rows=%d", len(rows))
	}
	got := decodeRecordedAccounting(t, rows[0].PayloadJSON)
	if got.TaskID == "" || got.Provider != "gateway-route" || got.Model != "claude/gpt-Gemini Production Alias" || got.RateCard.Reference.Revision != "frozen-v1" || got.ConfiguredEstimate == nil || got.ReportedCost == nil || !got.ReportedCost.Reported || got.ReportedCost.Amount != 0 || got.ReportedCost.Source != "" {
		t.Fatal("default alias lost actual target or separate zero cost")
	}
	if strings.Contains(string(rows[0].PayloadJSON), "untrusted-call-canary") {
		t.Fatal("reported call ID persisted")
	}
}

func TestRecordedAccountingSwarmExcludesActualChildDispatch(t *testing.T) {
	store := recordedAccountingStore(t)
	var parents, children atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		child := false
		for _, message := range request.Messages {
			if message.Role == "user" && strings.Contains(string(message.Content), "recorded child assignment") {
				child = true
			}
		}
		if child {
			children.Add(1)
			completionFixture(w, "Read-only child report complete.", "")
			return
		}
		if parents.Add(1) == 1 {
			completionFixture(w, "", `{"prompt":"recorded child assignment","mode":"explore"}`)
			return
		}
		completionFixture(w, "Read-only parent report complete.", "")
	}))
	t.Cleanup(server.Close)
	sink := &childRateCardSink{}
	live := automaticFixtureRunner(t, server.URL, engine.WithReadOnlyTasks(), engine.WithLiveSink(sink))
	live.SetProviderSpec(live.RunTarget().Provider, engine.ProviderSpec{Name: "openai", Model: "fixture-model"})
	live.SetLimits(1024, 8, 2, 2)
	goal := engine.NewSwarm("capture root only")
	var rootID string
	if _, err := goal.AddRecorded("parent", live, "Delegate inspection and summarize; no changes.", func(ctx context.Context, a *engine.RecordedSwarmActivation) error {
		rootID = string(a.Spec.ID)
		return a.RunRecorded(ctx, store, "session").Err
	}); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	_, rows := recordedAccountingSnapshot(t, live, "session")
	if children.Load() != 1 || parents.Load() < 2 || len(rows) != int(parents.Load()) {
		t.Fatalf("parents=%d children=%d captured=%d", parents.Load(), children.Load(), len(rows))
	}
	for _, row := range rows {
		if got := decodeRecordedAccounting(t, row.PayloadJSON); got.TaskID != rootID {
			t.Fatal("child settlement captured as root")
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	found := false
	for _, event := range sink.events {
		if event.Depth == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("child never settled")
	}
}

func TestRecordedAccountingReadRejectsUnsupportedAndInvalidPayload(t *testing.T) {
	for _, tc := range []struct {
		name, old, value string
		want             error
	}{
		{"unsupported version", `"version":1`, `"version":2`, engine.ErrRecordedProviderAccountingVersion},
		{"invalid scalar", `"attempt":1`, `"attempt":0`, engine.ErrInvalidRecordedProviderAccounting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := recordedAccountingStore(t)
			live := reservationRunner(t, &requestRecordingProvider{})
			if err := live.RunRecordedTurn(t.Context(), "go", nil, store, "session"); err != nil {
				t.Fatal(err)
			}
			_, rows := recordedAccountingSnapshot(t, live, "session")
			bad := rows[0].Clone()
			bad.PayloadJSON = bytes.Replace(bad.PayloadJSON, []byte(tc.old), []byte(tc.value), 1)
			if err := store.SaveSessionProviderAccounting(t.Context(), "session", []db.SessionProviderAccounting{bad}); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReadRecordedProviderAccounting(t.Context(), store, "session", bad.RequestBatchID); !errors.Is(err, tc.want) {
				t.Fatalf("read error=%v want=%v", err, tc.want)
			}
			// Rejection cannot repair/reprice the stored bytes.
			durable, err := store.GetSessionProviderAccounting(t.Context(), "session", bad.RequestBatchID)
			if err != nil || !bytes.Equal(durable.Accounting.PayloadJSON, bad.PayloadJSON) {
				t.Fatal("read repaired unsupported accounting", err)
			}
		})
	}
}
