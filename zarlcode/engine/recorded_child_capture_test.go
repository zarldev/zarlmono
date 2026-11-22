package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type childCaptureSink struct {
	runner.NopSink
	mu sync.Mutex
}

func (*childCaptureSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*childCaptureSink) PlanUpdated(string, code.Plan)    {}
func (s *childCaptureSink) OnProviderAttemptSettled(_ context.Context, e runner.ProviderAttemptSettled) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Depth > 0 {
		if e.Usage != nil {
			e.Usage.TotalTokens = 99999
		}
		if e.RateCard != nil {
			e.RateCard.Currency = "MUTATED"
		}
		if e.Execution != nil {
			e.Execution.ReportedCost.Amount = 99999
		}
	}
}

func childSpawnChunk(arguments ...string) llm.CompletionChunk {
	calls := make([]llm.ToolCall, len(arguments))
	for i, args := range arguments {
		calls[i] = llm.ToolCall{ID: string(rune('a' + i)), Type: "function", Function: llm.ToolCallFunction{Name: "agent_spawn", Arguments: args}}
	}
	return llm.CompletionChunk{ToolCalls: calls, FinishReason: llm.FinishReasons.TOOLCALLS}
}

func TestRecordedChildCaptureConcurrentRecursiveRetriesAndExactACK(t *testing.T) {
	store := recordedAccountingStore(t)
	sink := &childCaptureSink{}
	fixture := recordedAccountingFixture()
	var mu sync.Mutex
	calls := make(map[taskscope.ID]int)
	entered := 0
	siblings := make(chan struct{})
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		id, depth := taskscope.IDFrom(ctx), taskscope.DepthFrom(ctx)
		mu.Lock()
		calls[id]++
		attempt := calls[id]
		if depth == 1 && attempt == 1 {
			entered++
			if entered == 2 {
				close(siblings)
			}
		}
		mu.Unlock()
		return func(yield func(llm.CompletionChunk, error) bool) {
			if depth == 1 && attempt == 1 {
				select {
				case <-siblings:
				case <-ctx.Done():
					return
				}
			}
			if !yield(llm.CompletionChunk{UsageReported: true, Usage: *fixture.Usage, PricingUsage: fixture.PricingUsage,
				Execution: &llm.ExecutionObservation{GatewayCallID: "duplicate-provider-id-canary", GatewayDeployment: "deployment-canary", ReportedCost: llm.ReportedCost{Reported: true, Amount: 7, Currency: "JPY", Source: "private-source-canary"}}}, nil) {
				return
			}
			if attempt == 1 && depth < 2 {
				yield(llm.CompletionChunk{}, &llm.RateLimitError{Retryable: true, RetryAfter: time.Nanosecond})
				return
			}
			switch {
			case depth == 0 && attempt == 2:
				yield(childSpawnChunk(`{"prompt":"identical child text canary","mode":"explore"}`, `{"prompt":"identical child text canary","mode":"explore"}`), nil)
			case depth == 1 && attempt == 2:
				yield(childSpawnChunk(`{"prompt":"grandchild text canary","mode":"explore"}`), nil)
			default:
				yield(llm.CompletionChunk{Content: "Read-only evidence is complete.", FinishReason: llm.FinishReasons.STOP}, nil)
			}
		}
	})
	live := requestControlRunner(t, provider, t.TempDir(), engine.WithReadOnlyTasks(), engine.WithLiveSink(sink))
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "actual-route", Model: "frozen-model"}, RateCard: fixture.RateCard})
	live.SetLimits(1024, 12, 8, 3)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := live.RunRecordedTurn(ctx, "delegate read-only evidence", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	history, roots, children, err := live.RecordedHistoryWithAccounting("session")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) < 3 || len(children) < 8 {
		t.Fatalf("roots=%d children=%d", len(roots), len(children))
	}
	owner := roots[0].RequestBatchID
	if owner == roots[1].RequestBatchID {
		t.Fatal("retry reused receipt")
	}
	byTask := make(map[string]int)
	for _, row := range children {
		record, err := engine.DecodeRecordedChildProviderAccounting(row.PayloadJSON)
		if err != nil || record.OwnerRequestBatchID != owner || record.ChildTaskID != row.ChildTaskID || record.Attempt != byTask[row.ChildTaskID]+1 {
			t.Fatal("child identity/first anchor", err)
		}
		byTask[row.ChildTaskID]++
		if record.Provider != "actual-route" || record.Model != "frozen-model" || record.Usage.TotalTokens != fixture.Usage.TotalTokens || record.RateCard.Currency != fixture.RateCard.Currency || record.ReportedCost.Amount != 7 || record.ReportedCost.Source != "" || !reflect.DeepEqual(record.ConfiguredEstimate, fixture.ConfiguredEstimate) {
			t.Fatalf("capture mutated/repriced: %+v", record)
		}
		for _, forbidden := range []string{"duplicate-provider-id-canary", "deployment-canary", "private-source-canary", "identical child text canary", "grandchild text canary"} {
			if bytes.Contains(row.PayloadJSON, []byte(forbidden)) {
				t.Fatal("privacy canary retained")
			}
		}
	}
	if len(byTask) != 4 {
		t.Fatalf("distinct UUID invocations=%d", len(byTask))
	}
	frozen := make([]db.SessionChildProviderAccounting, len(children))
	for i, row := range children {
		frozen[i] = row.Clone()
	}
	// Returned snapshots own all nested bytes, and altered identity/content ACKs
	// cannot release a pending attempt. Root-only legacy ACKs cannot release it.
	history[0].Messages[0][0] ^= 1
	for _, mutate := range []func(*db.SessionChildProviderAccounting){
		func(r *db.SessionChildProviderAccounting) { r.OwnerRequestBatchID = "wrong" },
		func(r *db.SessionChildProviderAccounting) { r.ChildTaskID = "wrong" },
		func(r *db.SessionChildProviderAccounting) { r.Attempt++ },
		func(r *db.SessionChildProviderAccounting) { r.Version++ },
		func(r *db.SessionChildProviderAccounting) { r.PayloadJSON[0] = '!' },
	} {
		changed := children[0].Clone()
		mutate(&changed)
		live.AcknowledgeRecordedHistoryWithAccounting("session", nil, nil, []db.SessionChildProviderAccounting{changed})
	}
	live.AcknowledgeRecordedProviderAccounting("session", roots)
	h, _, pending, err := live.RecordedHistoryWithAccounting("session")
	if err != nil || !reflect.DeepEqual(pending, frozen) || reflect.DeepEqual(h, history) {
		t.Fatal("snapshot aliases or stale ACK dropped attempts", err)
	}
	// Persist without root settlements: a child anchors a real request receipt,
	// not a root-accounting row. Retried bytes remain idempotent after reopen.
	record, err := store.GetSession(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.CommitCompletedTurnWithAccounting(t.Context(), record, db.TranscriptUpdate{SessionID: "session", Workspace: "/workspace"}, nil, pending, h...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range pending {
		got, err := engine.ReadRecordedChildProviderAccounting(t.Context(), store, "session", row.ChildTaskID, row.Attempt)
		if err != nil || got.OwnerRequestBatchID != owner {
			t.Fatal(err)
		}
	}
	// A later operation reuses attempt ordinals but not UUID or owner identity.
	if err := live.RunRecordedTurn(t.Context(), "another root", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	live.AcknowledgeRecordedHistoryWithAccounting("session", h, roots, pending)
	_, laterRoots, laterChildren, err := live.RecordedHistoryWithAccounting("session")
	if err != nil || len(laterRoots) < 3 || len(laterChildren) != len(frozen) {
		t.Fatal("ACK crossed later boundary", err)
	}
}

func TestRecordedChildDeniedUninvokedAndCaptureDisabled(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		t.Run(map[bool]string{false: "not recorded", true: "denied named target"}[recorded], func(t *testing.T) {
			store := recordedAccountingStore(t)
			n := 0
			provider := recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				n++
				return func(yield func(llm.CompletionChunk, error) bool) {
					if n == 1 {
						yield(childSpawnChunk(`{"agent":"missing-agent","prompt":"do not run","mode":"explore"}`), nil)
					} else {
						yield(llm.CompletionChunk{Content: "Cannot delegate; no actions taken.", FinishReason: llm.FinishReasons.STOP}, nil)
					}
				}
			})
			settings := newJudgeTestSettings(t)
			for key, value := range map[string]string{prefs.KeySpawnFallback: "error", prefs.KeyDecomposeJudge: "off"} {
				if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
					t.Fatal(err)
				}
			}
			live := requestControlRunner(t, provider, settings.WorkspaceRoot(), engine.WithReadOnlyTasks(), engine.WithSettings(settings))
			live.SetLimits(1024, 3, 1, 1)
			var err error
			if recorded {
				err = live.RunRecordedTurn(t.Context(), "delegate", nil, store, "session")
			} else {
				err = live.RunTurn(t.Context(), "delegate")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, roots, children, err := live.RecordedHistoryWithAccounting("session")
			if err != nil || len(children) != 0 || (!recorded && len(roots) != 0) {
				t.Fatal("denied/unrecorded evidence", err)
			}
		})
	}
}

func TestRecordedChildReaderRejectsEnvelopeIdentityMismatch(t *testing.T) {
	for _, field := range []string{"owner", "task", "attempt", "version"} {
		t.Run(field, func(t *testing.T) {
			store := recordedAccountingStore(t)
			record := engine.RecordedChildProviderAccounting{OwnerRequestBatchID: "owner", ChildTaskID: "task", Attempt: 1}
			other := record
			switch field {
			case "owner":
				other.OwnerRequestBatchID = "other"
			case "task":
				other.ChildTaskID = "other"
			case "attempt":
				other.Attempt = 2
			}
			payload, err := engine.EncodeRecordedChildProviderAccounting(other)
			if err != nil {
				t.Fatal(err)
			}
			if field == "version" {
				var raw map[string]any
				if err := json.Unmarshal(payload, &raw); err != nil {
					t.Fatal(err)
				}
				raw["version"] = 2
				payload, err = json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveSessionHistory(t.Context(), "session", []db.SessionHistoryBatch{{ID: "owner", RequestJSON: []byte(`{}`)}}); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveSessionChildProviderAccounting(t.Context(), "session", []db.SessionChildProviderAccounting{{OwnerRequestBatchID: "owner", ChildTaskID: "task", Attempt: 1, Version: 1, PayloadJSON: payload}}); err != nil {
				t.Fatal(err)
			}
			_, err = engine.ReadRecordedChildProviderAccounting(t.Context(), store, "session", "task", 1)
			want := engine.ErrInvalidRecordedChildProviderAccounting
			if field == "version" {
				want = engine.ErrRecordedChildProviderAccountingVersion
			}
			if !errors.Is(err, want) {
				t.Fatal("unsafe reader identity", err)
			}
		})
	}
}

func TestRecordedChildSettledErrorRetainsUnknownEvidence(t *testing.T) {
	for _, reported := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreported", true: "partial"}[reported], func(t *testing.T) {
			store := recordedAccountingStore(t)
			var rootCalls atomic.Int32
			provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				depth := taskscope.DepthFrom(ctx)
				call := int32(0)
				if depth == 0 {
					call = rootCalls.Add(1)
				}
				return func(yield func(llm.CompletionChunk, error) bool) {
					if depth > 0 {
						if reported {
							yield(llm.CompletionChunk{PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 5, Known: true}}}}, nil)
						}
						yield(llm.CompletionChunk{}, errors.New("synthetic-private-provider-error-canary"))
						return
					}
					if call == 1 {
						yield(childSpawnChunk(`{"prompt":"child error fixture","mode":"explore"}`), nil)
					} else {
						yield(llm.CompletionChunk{Content: "Read-only result recorded.", FinishReason: llm.FinishReasons.STOP}, nil)
					}
				}
			})
			live := requestControlRunner(t, provider, t.TempDir(), engine.WithReadOnlyTasks())
			live.SetLimits(1024, 5, 3, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := live.RunRecordedTurn(ctx, "delegate read-only fixture", nil, store, "session"); err != nil {
				t.Fatal(err)
			}
			_, roots, children, err := live.RecordedHistoryWithAccounting("session")
			if err != nil || len(roots) == 0 || len(children) != 1 {
				t.Fatalf("roots=%d children=%d err=%v", len(roots), len(children), err)
			}
			child, err := engine.DecodeRecordedChildProviderAccounting(children[0].PayloadJSON)
			if err != nil || child.Usage != nil || child.ReportedCost != nil || child.ConfiguredEstimate != nil || ((child.PricingUsage != nil) != reported) {
				t.Fatal("error inferred evidence", err)
			}
			if bytes.Contains(children[0].PayloadJSON, []byte("synthetic-private-provider-error-canary")) {
				t.Fatal("error text persisted")
			}
		})
	}
}
