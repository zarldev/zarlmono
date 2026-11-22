package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func headlessSQL(t *testing.T, store *db.Store, query string) {
	t.Helper()
	if _, err := store.DB().ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}

func headlessReport(number int, content string, tokens int) pursue.AttemptReport {
	return pursue.AttemptReport{Attempt: pursue.Attempt{Number: number, Spec: runner.TaskSpec{Prompt: "frozen prompt"},
		Result: runner.TaskResult{Reason: runner.TerminalCompleted, FinalContent: content, TotalUsage: &llm.Usage{PromptTokens: tokens}}}, Decision: pursue.Decision{Done: true}}
}

func TestHeadlessAccountingRecorderRetainsFrozenStartSummariesAndCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "retry.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		rec := engine.NewHeadlessRecorder(store, "retry", t.TempDir())
		headlessSQL(t, store, `CREATE TRIGGER reject_start BEFORE INSERT ON headless_runs BEGIN SELECT RAISE(ABORT,'private diagnostic'); END`)
		startAt := time.Now()
		rec.Start(t.Context(), "original", "route", "model")
		if _, err := store.GetHeadlessRun(t.Context(), "retry"); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("failed start became durable", err)
		}
		time.Sleep(2 * time.Second)
		headlessSQL(t, store, `DROP TRIGGER reject_start`)
		headlessSQL(t, store, `CREATE TRIGGER reject_attempt BEFORE INSERT ON headless_attempts BEGIN SELECT RAISE(ABORT,'private diagnostic'); END`)
		report := headlessReport(7, "original summary", 13) // public API is not private Drive numbering
		attemptAt := time.Now()
		rec.Attempt(t.Context(), report)
		report.Attempt.Result.TotalUsage.PromptTokens = 999
		if _, err := store.GetHeadlessRun(t.Context(), "retry"); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("atomic failure leaked start", err)
		}
		time.Sleep(2 * time.Second)
		headlessSQL(t, store, `DROP TRIGGER reject_attempt`)
		rec.Attempt(t.Context(), headlessReport(8, "next", 0))
		got, err := store.GetHeadlessRun(t.Context(), "retry")
		if err != nil || got.Prompt != "original" || got.Provider != "route" || got.StartedAt.Unix() != startAt.Unix() {
			t.Fatalf("start retry changed identity: %+v: %v", got, err)
		}
		attempts, err := store.ListHeadlessAttempts(t.Context(), "retry")
		if err != nil || len(attempts) != 2 || attempts[0].RecordedAt.Unix() != attemptAt.Unix() || attempts[0].FinalContent != "original summary" || *attempts[0].TokensIn != 13 || attempts[1].TokensIn == nil || *attempts[1].TokensIn != 0 {
			t.Fatalf("summary retry changed frozen values: %+v: %v", attempts, err)
		}
		// A second public call with the same number still replaces the summary.
		rec.Attempt(t.Context(), headlessReport(7, "replacement", 23))
		attempts, err = store.ListHeadlessAttempts(t.Context(), "retry")
		if err != nil || attempts[0].FinalContent != "replacement" || *attempts[0].TokensIn != 23 {
			t.Fatal("legacy UPSERT changed", err)
		}
		headlessSQL(t, store, `CREATE TRIGGER reject_completion BEFORE UPDATE OF ended_at ON headless_runs BEGIN SELECT RAISE(ABORT,'private diagnostic'); END`)
		completeAt := time.Now()
		completion := runner.TaskResult{Reason: runner.TerminalCompleted, FinalContent: "frozen completion", TotalUsage: &llm.Usage{PromptTokens: 31}}
		rec.Complete(t.Context(), completion)
		completion.TotalUsage.PromptTokens = 999
		time.Sleep(2 * time.Second)
		headlessSQL(t, store, `DROP TRIGGER reject_completion`)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		rec.Complete(cancelled, runner.TaskResult{Reason: runner.TerminalError, FinalContent: "must not replace"})
		got, err = store.GetHeadlessRun(t.Context(), "retry")
		if err != nil || got.EndedAt == nil || got.EndedAt.Unix() != completeAt.Unix() || got.Duration != completeAt.Sub(startAt) || got.FinalContent != "frozen completion" || *got.TokensIn != 31 {
			t.Fatalf("completion retry changed frozen values: %+v: %v", got, err)
		}
	})
}

// Wait for a real public database/sql queue, not a guessed scheduling delay.
func waitHeadlessWriter(t *testing.T, store *db.Store, before int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for store.DB().Stats().WaitCount <= before {
		if ctx.Err() != nil {
			t.Fatal("writer did not queue", ctx.Err())
		}
		runtime.Gosched()
	}
}

func TestHeadlessAccountingRecorderConcurrentReplacementSurvivesOldACK(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rec := engine.NewHeadlessRecorder(store, "concurrent", t.TempDir())
	rec.Start(t.Context(), "p", "route", "model")
	conn, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	closeConn := sync.OnceFunc(func() { _ = conn.Close() })
	defer closeConn()
	before := store.DB().Stats().WaitCount
	first, second := make(chan struct{}), make(chan struct{})
	testCtx := t.Context()
	go func() { defer close(first); rec.Attempt(testCtx, headlessReport(1, "older", 1)) }()
	waitHeadlessWriter(t, store, before)
	go func() { defer close(second); rec.Attempt(testCtx, headlessReport(1, "newer", 2)) }()
	// The second call has no database queue until the first writer leaves. Its
	// eventual checkpoint must still persist the newer arrival, even if an old
	// transaction acknowledged attempt 1 while it was pending.
	closeConn()
	<-first
	<-second
	attempts, err := store.ListHeadlessAttempts(t.Context(), "concurrent")
	if err != nil || len(attempts) != 1 || attempts[0].FinalContent != "newer" || *attempts[0].TokensIn != 2 {
		t.Fatalf("stale ACK/reordering lost replacement: %+v: %v", attempts, err)
	}
}

type headlessTerminalSink struct {
	runner.NopSink
	panicAfter bool
	settled    chan struct{}
	cancel     context.CancelFunc
}

func (*headlessTerminalSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*headlessTerminalSink) PlanUpdated(string, code.Plan)    {}
func (s *headlessTerminalSink) OnConversationStarted(context.Context, runner.ConversationStarted) {
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *headlessTerminalSink) OnProviderAttemptSettled(context.Context, runner.ProviderAttemptSettled) {
	if s.settled != nil {
		close(s.settled)
	}
	if s.panicAfter {
		panic("downstream panic after capture")
	}
}

func TestHeadlessAccountingZeroProviderFailurePanicAndPoison(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		maxIter                                   int
		providerPanic, sinkPanic, cancel, invalid bool
		wantRows                                  int
		wantReason                                runner.TerminalReason
	}{
		{name: "zero provider invalid iterations", maxIter: -1, wantReason: runner.TerminalError},
		{name: "zero provider cancellation", maxIter: 1, cancel: true, wantReason: runner.TerminalError},
		{name: "panic before settlement", maxIter: 1, providerPanic: true, wantReason: runner.TerminalError},
		{name: "panic after settlement", maxIter: 1, sinkPanic: true, wantRows: 1, wantReason: runner.TerminalError},
		{name: "invalid accounting does not change verdict", maxIter: 1, invalid: true, wantReason: runner.TerminalCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					if tc.providerPanic {
						panic("before settlement")
					}
					u := llm.Usage{}
					if tc.invalid {
						u.TotalTokens = -1
					}
					yield(llm.CompletionChunk{Content: "done", UsageReported: true, Usage: u}, nil)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sink := &headlessTerminalSink{panicAfter: tc.sinkPanic}
			if tc.cancel {
				sink.cancel = cancel
			}
			live, store, _ := headlessAccountingLive(t, provider, engine.WithLiveSink(sink))
			result := live.RunHeadless(ctx, "terminal path", tc.maxIter)
			if result.Reason != tc.wantReason {
				t.Fatalf("reason=%s err=%v", result.Reason, result.Err)
			}
			rows := headlessRows(t, store, string(result.ID))
			if len(rows) != tc.wantRows {
				t.Fatalf("accounting=%d", len(rows))
			}
			run, err := store.GetHeadlessRun(t.Context(), string(result.ID))
			if err != nil || (run.EndedAt == nil) != tc.invalid {
				t.Fatalf("completion cannot claim accounting success after rejected capture: %+v: %v", run, err)
			}
			if tc.wantRows == 1 {
				got := decodeRecordedHeadlessAccounting(t, rows[0].Accounting.PayloadJSON)
				if got.PursueAttempt != 1 || got.ProviderOrdinal != 1 || got.Usage == nil || got.Usage.TotalTokens != 0 {
					t.Fatal("panic lost identity/known zero")
				}
			}
		})
	}
}

func TestHeadlessAccountingRetryPendingRowsAfterAttemptFailure(t *testing.T) {
	var store *db.Store
	var root string
	calls := 0
	provider := recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		calls++
		call := calls
		return func(yield func(llm.CompletionChunk, error) bool) {
			if call == 2 {
				headlessSQL(t, store, `DROP TRIGGER reject_attempt`)
				if err := os.WriteFile(filepath.Join(root, "verified"), nil, 0o600); err != nil {
					t.Error(err)
				}
			}
			yield(llm.CompletionChunk{Content: "done", UsageReported: true, Usage: llm.Usage{TotalTokens: call * 17}}, nil)
		}
	})
	live, database, workspace := headlessAccountingLive(t, provider)
	store, root = database, workspace
	if out, err := exec.CommandContext(t.Context(), "git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	live.SetVerifyLoop("test -f verified", 2)
	headlessSQL(t, store, `CREATE TRIGGER reject_attempt BEFORE INSERT ON headless_attempts BEGIN SELECT RAISE(ABORT,'rejected'); END`)
	result := live.RunHeadless(t.Context(), "attempt retry", 1)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	rows := headlessRows(t, store, string(result.ID))
	attempts, err := store.ListHeadlessAttempts(t.Context(), string(result.ID))
	if err != nil || len(rows) != 2 || len(attempts) != 2 {
		t.Fatalf("pending retry rows=%d summaries=%d: %v", len(rows), len(attempts), err)
	}
	for i, row := range rows {
		got := decodeRecordedHeadlessAccounting(t, row.Accounting.PayloadJSON)
		if got.PursueAttempt != i+1 || got.ProviderOrdinal != 1 || got.Usage.TotalTokens != (i+1)*17 {
			t.Fatalf("pending row lost/reconstructed: %+v", got)
		}
	}
}

type headlessLateProvider struct{ entered, cancelled, release chan struct{} }

func (*headlessLateProvider) Name() string { return "openai" }
func (p *headlessLateProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.entered)
		<-ctx.Done()
		close(p.cancelled)
		<-p.release
		yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 19, TotalTokens: 19}}, nil)
		yield(llm.CompletionChunk{}, ctx.Err())
	}
}

func TestHeadlessAccountingSyntheticReportKeepsLateLeaseAndJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &headlessLateProvider{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
		computer := &fakeComputerSession{}
		live, store, root := headlessAccountingLive(t, p, engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) { return computer, nil }))
		if out, err := exec.CommandContext(t.Context(), "git", "init", "--quiet", root).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		ready := filepath.Join(root, "ready")
		live.SetEarlyStopCommand([]string{"test", "-f", ready})
		if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); err != nil {
			t.Fatal(err)
		}
		release := sync.OnceFunc(func() { close(p.release) })
		done := make(chan runner.TaskResult, 1)
		testCtx := t.Context()
		go func() { done <- live.RunHeadless(testCtx, "late usage", 1) }()
		joined := false
		defer func() {
			release()
			if !joined {
				<-done
			}
		}()
		<-p.entered
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		<-p.cancelled
		time.Sleep(time.Minute)
		synctest.Wait()
		runs, err := store.ListHeadlessRunsByWorkspace(t.Context(), root, 10)
		if err != nil || len(runs) != 1 || runs[0].EndedAt != nil {
			t.Fatalf("premature completion: %+v: %v", runs, err)
		}
		attempts, err := store.ListHeadlessAttempts(t.Context(), runs[0].ID)
		if err != nil || len(attempts) != 1 || attempts[0].TokensIn != nil {
			t.Fatalf("synthetic summary: %+v: %v", attempts, err)
		}
		frozen := attempts[0]
		if len(headlessRows(t, store, runs[0].ID)) != 0 {
			t.Fatal("fabricated provider row")
		}
		select {
		case <-done:
			joined = true
			t.Fatal("returned before provider settled")
		default:
		}
		closeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := live.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("close abandoned owner", err)
		}
		if computer.closeCalls != 0 {
			t.Fatal("closed dependencies while attempt alive")
		}
		release()
		result := <-done
		joined = true
		if !errors.Is(result.Err, pursue.ErrAttemptCancelDrainTimeout) || result.Reason != runner.TerminalError {
			t.Fatalf("verdict changed: %+v", result)
		}
		rows := headlessRows(t, store, runs[0].ID)
		if len(rows) != 1 {
			t.Fatalf("late rows=%d", len(rows))
		}
		got := decodeRecordedHeadlessAccounting(t, rows[0].Accounting.PayloadJSON)
		if got.PursueAttempt != 1 || got.ProviderOrdinal != 1 || got.Usage.PromptTokens != 19 {
			t.Fatalf("late identity/usage: %+v", got)
		}
		attempts, err = store.ListHeadlessAttempts(t.Context(), runs[0].ID)
		if err != nil || len(attempts) != 1 || !reflect.DeepEqual(attempts[0], frozen) {
			t.Fatal("eventual result rewrote synthetic summary", err)
		}
		run, err := store.GetHeadlessRun(t.Context(), runs[0].ID)
		if err != nil || run.EndedAt == nil || run.TokensIn == nil || *run.TokensIn != 19 || !strings.Contains(run.Error, "drain timeout") {
			t.Fatalf("final flush: %+v: %v", run, err)
		}
	})
}

func TestHeadlessAccountingBuildFailureRetriesCancelledStart(t *testing.T) {
	provider := recordedAccountingProvider(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		t.Error("provider invoked after cancelled construction")
		return func(func(llm.CompletionChunk, error) bool) {}
	})
	live, store, _ := headlessAccountingLive(t, provider)
	conn, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	closeConn := sync.OnceFunc(func() { _ = conn.Close() })
	defer closeConn()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	before := store.DB().Stats().WaitCount
	done := make(chan runner.TaskResult, 1)
	go func() { done <- live.RunHeadless(ctx, "cancel construction", 1) }()
	waitHeadlessWriter(t, store, before)
	cancel()
	closeConn()
	result := <-done
	if result.Reason != runner.TerminalError || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("build failure: %+v", result)
	}
	run, err := store.GetHeadlessRun(t.Context(), string(result.ID))
	if err != nil || run.EndedAt == nil || run.Prompt != "cancel construction" {
		t.Fatalf("detached completion did not retry start: %+v: %v", run, err)
	}
	attempts, err := store.ListHeadlessAttempts(t.Context(), string(result.ID))
	if err != nil || len(attempts) != 0 || len(headlessRows(t, store, string(result.ID))) != 0 {
		t.Fatal("unentered Run fabricated summaries or accounting", err)
	}
}

func TestHeadlessAccountingLateAppendDuringSyntheticCheckpoint(t *testing.T) {
	p := &headlessLateProvider{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	sink := &headlessTerminalSink{settled: make(chan struct{})}
	live, store, _ := headlessAccountingLive(t, p, engine.WithLiveSink(sink))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	release := sync.OnceFunc(func() { close(p.release) })
	done := make(chan runner.TaskResult, 1)
	go func() { done <- live.RunHeadless(ctx, "late append during database wait", 1) }()
	joined := false
	var closeConn func()
	defer func() {
		cancel()
		release()
		if closeConn != nil {
			closeConn()
		}
		if !joined {
			<-done
		}
	}()
	<-p.entered
	conn, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	closeConn = sync.OnceFunc(func() { _ = conn.Close() })
	before := store.DB().Stats().WaitCount
	cancel() // parent cancellation synthesizes a report while Run is still alive
	<-p.cancelled
	waitHeadlessWriter(t, store, before)
	// The synthetic checkpoint already snapshotted an empty row set and now
	// waits for SQLite. Let settlement append while that I/O remains blocked.
	release()
	<-sink.settled
	closeConn()
	result := <-done
	joined = true
	rows := headlessRows(t, store, string(result.ID))
	if len(rows) != 1 {
		t.Fatalf("old ACK lost late append: rows=%d", len(rows))
	}
	got := decodeRecordedHeadlessAccounting(t, rows[0].Accounting.PayloadJSON)
	if got.PursueAttempt != 1 || got.ProviderOrdinal != 1 || got.Usage.PromptTokens != 19 {
		t.Fatalf("late occurrence changed: %+v", got)
	}
	attempts, err := store.ListHeadlessAttempts(t.Context(), string(result.ID))
	if err != nil || len(attempts) != 1 || attempts[0].TokensIn != nil {
		t.Fatalf("late result rewrote synthetic summary: %+v: %v", attempts, err)
	}
	run, err := store.GetHeadlessRun(t.Context(), string(result.ID))
	if err != nil || run.EndedAt == nil || run.TokensIn == nil || *run.TokensIn != 19 {
		t.Fatalf("completion omitted late usage: %+v: %v", run, err)
	}
}

func TestHeadlessAccountingPublicRejectedLaunchHasSummaryWithoutOccurrence(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "rejected.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rec := engine.NewHeadlessRecorder(store, "rejected", t.TempDir())
	rec.Start(t.Context(), "p", "route", "model")
	supervisor := pursue.NewSupervisor()
	defer func() { _ = supervisor.Join(context.WithoutCancel(t.Context())) }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entries, goals := 0, 0
	req := pursue.NewRequest(func(context.Context, runner.TaskSpec) runner.TaskResult {
		entries++
		return runner.TaskResult{Reason: runner.TerminalCompleted}
	}, runner.TaskSpec{ID: "rejected"}, pursue.WithWatcher(func(context.Context) <-chan struct{} {
		cancel() // reject launch after watcher registration, before AttemptFunc entry
		return nil
	}), pursue.WithGoal(pursue.GoalFunc(func(context.Context, pursue.Attempt) pursue.Decision {
		goals++
		return pursue.Retry("must never run")
	})))
	out := supervisor.Drive(ctx, req, pursue.WithMaxAttempts(3), pursue.WithOnAttempt(func(report pursue.AttemptReport) {
		rec.Attempt(t.Context(), report)
	}))
	if err := supervisor.Join(t.Context()); err != nil {
		t.Fatal(err)
	}
	if entries != 0 || goals != 0 || out.Attempts != 1 || !errors.Is(out.Err(), pursue.ErrSupervisorClosed) {
		t.Fatalf("launch rejection retried/evaluated: entries=%d goals=%d outcome=%+v", entries, goals, out)
	}
	rec.Complete(t.Context(), out.Result)
	attempts, err := store.ListHeadlessAttempts(t.Context(), "rejected")
	if err != nil || len(attempts) != 1 || attempts[0].AttemptNumber != 1 || len(headlessRows(t, store, "rejected")) != 0 {
		t.Fatalf("rejection fabricated occurrence or number gap: %+v: %v", attempts, err)
	}
}

func TestHeadlessAccountingConflictRollsBackSummaryAndPreservesCLIExit(t *testing.T) {
	var store *db.Store
	var runID string
	conflict := db.HeadlessProviderAccounting{PursueAttempt: 1, ProviderOrdinal: 1, Version: 1, PayloadJSON: []byte(`{"existing":"immutable conflicting evidence"}`)}
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			runID = string(taskscope.IDFrom(ctx))
			run, err := store.GetHeadlessRun(ctx, runID)
			if err != nil {
				t.Error(err)
				return
			}
			start := db.HeadlessRunStart{ID: run.ID, Workspace: run.Workspace, BaseCommit: run.BaseCommit, Prompt: run.Prompt, StartedAt: run.StartedAt, Provider: run.Provider, Model: run.Model}
			if err := store.CommitHeadlessRecording(ctx, start, nil, []db.HeadlessProviderAccounting{conflict}, nil); err != nil {
				t.Error(err)
				return
			}
			yield(llm.CompletionChunk{Content: "done", UsageReported: true, Usage: llm.Usage{PromptTokens: 11}}, nil)
		}
	})
	live, database, _ := headlessAccountingLive(t, provider)
	store = database
	out, err := os.Create(filepath.Join(t.TempDir(), "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	if exit := engine.RunHeadlessProcess(t.Context(), live, "private prompt", 1, out); exit != 0 {
		t.Fatalf("recording conflict changed successful CLI exit: %d", exit)
	}
	run, err := store.GetHeadlessRun(t.Context(), runID)
	if err != nil || run.EndedAt != nil {
		t.Fatalf("conflicting final flush partially completed run: %+v: %v", run, err)
	}
	attempts, err := store.ListHeadlessAttempts(t.Context(), runID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("conflict leaked summary UPSERT: %+v: %v", attempts, err)
	}
	rows := headlessRows(t, store, runID)
	if len(rows) != 1 || !bytes.Equal(rows[0].Accounting.PayloadJSON, conflict.PayloadJSON) {
		t.Fatal("conflict replaced immutable occurrence")
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, added := fields["run_id"]; added {
		t.Fatal("unapproved run_id report field")
	}
	var report engine.HeadlessReport
	if err := json.Unmarshal(data, &report); err != nil || report.TerminalReason != runner.TerminalCompleted || report.TotalUsage.PromptTokens != 11 {
		t.Fatalf("recording failure changed legacy report: %+v: %v", report, err)
	}
}
