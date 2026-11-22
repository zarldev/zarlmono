package engine_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

type headlessChildSettledSink struct {
	*headlessAuxiliaryTerminalSink
	settled func()
}

func (s *headlessChildSettledSink) OnProviderAttemptSettled(ctx context.Context, event runner.ProviderAttemptSettled) {
	if event.Depth > 0 {
		s.settled()
	}
}

func TestHeadlessChildLateAfterRootTerminalAndReopen(t *testing.T) {
	childEntered, release, childSettled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	markEntered := sync.OnceFunc(func() { close(childEntered) })
	markSettled := sync.OnceFunc(func() { close(childSettled) })
	baseSink := &headlessAuxiliaryTerminalSink{terminal: make(chan struct{}), release: make(chan struct{})}
	sink := &headlessChildSettledSink{headlessAuxiliaryTerminalSink: baseSink, settled: markSettled}
	unblockTerminal := sync.OnceFunc(func() { close(sink.release) })
	var roots atomic.Int32
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		if taskscope.DepthFrom(ctx) > 0 {
			return func(yield func(llm.CompletionChunk, error) bool) {
				markEntered()
				<-release
				yield(llm.CompletionChunk{Content: "Read-only evidence complete.", FinishReason: llm.FinishReasons.STOP, UsageReported: true, Usage: llm.Usage{PromptTokens: 29, TotalTokens: 29},
					Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true, Amount: 3, Currency: "USD", Source: "private-source"}}}, nil)
			}
		}
		call := roots.Add(1)
		return func(yield func(llm.CompletionChunk, error) bool) {
			if call == 1 {
				yield(childSpawnChunk(`{"prompt":"late private child","mode":"explore"}`), nil)
				return
			}
			select {
			case <-childEntered:
			case <-ctx.Done():
				return
			}
			yield(llm.CompletionChunk{Content: "root complete", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	live, store, _ := headlessChildLive(t, provider, engine.WithLiveSink(sink))
	live.SetLimits(1024, 6, 6, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan runner.TaskResult, 1)
	go func() { done <- live.RunHeadless(ctx, "late child", 6) }()
	var closeConn func()
	joined := false
	defer func() {
		cancel()
		unblock()
		unblockTerminal()
		if closeConn != nil {
			closeConn()
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-sink.terminal:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-childSettled:
		t.Fatal("child settled before root terminal")
	default:
	}
	conn, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closeConn = sync.OnceFunc(func() { _ = conn.Close() })
	before := store.DB().Stats().WaitCount
	unblockTerminal()
	waitHeadlessWriter(t, store, before)
	// An earlier checkpoint owns an empty child snapshot while I/O is blocked.
	unblock()
	select {
	case <-childSettled:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-done:
		joined = true
		t.Fatal("returned through held writer")
	default:
	}
	closeConn()
	result := <-done
	joined = true
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	rows := headlessChildRows(t, store, string(result.ID))
	if len(rows) != 1 {
		t.Fatalf("late child rows=%d", len(rows))
	}
	got, err := engine.DecodeRecordedHeadlessChildProviderAccounting(rows[0].Accounting.PayloadJSON)
	if err != nil || got.Usage.PromptTokens != 29 || got.ReportedCost.Source != "" {
		t.Fatalf("late evidence: %+v %v", got, err)
	}
	var dbPath string
	if err := store.DB().QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if saved := headlessChildRows(t, reopened, string(result.ID)); len(saved) != 1 || saved[0].Sequence != rows[0].Sequence {
		t.Fatalf("reopened rows=%+v", saved)
	}
}

func TestHeadlessChildCheckpointFailureLeavesVerdictAndNoRow(t *testing.T) {
	var roots atomic.Int32
	provider := recordedAccountingProvider(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
		depth := taskscope.DepthFrom(ctx)
		var call int32
		if depth == 0 {
			call = roots.Add(1)
		}
		return func(yield func(llm.CompletionChunk, error) bool) {
			if depth > 0 {
				yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 7, TotalTokens: 7}}, nil)
				yield(llm.CompletionChunk{Content: "child", FinishReason: llm.FinishReasons.STOP}, nil)
				return
			}
			if call == 1 {
				yield(childSpawnChunk(`{"prompt":"child","mode":"explore"}`), nil)
				return
			}
			yield(llm.CompletionChunk{Content: "root completed", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	})
	live, store, _ := headlessChildLive(t, provider)
	live.SetLimits(1024, 6, 4, 2)
	headlessSQL(t, store, `CREATE TRIGGER reject_child BEFORE INSERT ON headless_child_provider_accounting BEGIN SELECT RAISE(ABORT,'private diagnostic'); END`)
	result := live.RunHeadless(t.Context(), "run", 6)
	if result.Err != nil || result.Reason != runner.TerminalMaxIterations || result.FinalContent != "root completed" {
		t.Fatalf("recording changed verdict: reason=%s content=%q err=%v", result.Reason, result.FinalContent, result.Err)
	}
	if rows := headlessChildRows(t, store, string(result.ID)); len(rows) != 0 {
		t.Fatalf("failed checkpoint durable: %+v", rows)
	}
	if run, err := store.GetHeadlessRun(t.Context(), string(result.ID)); err != nil || run.EndedAt != nil {
		t.Fatalf("failed accounting batch changed durable start/terminal: %+v %v", run, err)
	}
	if attempts, err := store.ListHeadlessAttempts(t.Context(), string(result.ID)); err != nil || len(attempts) != 0 {
		t.Fatalf("failed accounting batch leaked summaries: %+v %v", attempts, err)
	}
}
