package swarm_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func finding(t *testing.T, summary string) board.Draft {
	t.Helper()
	draft, err := board.ParseDraft(summary, "full evidence not injected", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func launch(t *testing.T, goal *swarm.Swarm) {
	t.Helper()
	ctx := t.Context()
	done := make(chan error, 1)
	go func() { done <- goal.Run(ctx) }()
	t.Cleanup(func() {
		if err := goal.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, swarm.ErrClosed) && !errors.Is(err, swarm.ErrBudget) && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
}

func runTurn(ctx context.Context, activation swarm.Activation) runner.TaskResult {
	inputs := activation.Inputs(nil)
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("finding considered")}})
	r := runner.New(client, runner.WithInputSource(inputs))
	return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult { return r.Run(ctx, activation.Spec) })
}

func add(t *testing.T, goal *swarm.Swarm, id swarm.AgentID, execute swarm.Execute) swarm.Member {
	t.Helper()
	member, err := goal.Add(swarm.Agent{ID: id, Task: runner.TaskSpec{Prompt: "original assignment"}, Execute: execute})
	if err != nil {
		t.Fatal(err)
	}
	return member
}

func waitIdle(t *testing.T, goal *swarm.Swarm) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostWakesIdlePeerWithHistoryAndNotAuthor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("shared goal")
		author := add(t, goal, "author", runTurn)
		add(t, goal, "reader", runTurn)
		launch(t, goal)
		waitIdle(t, goal)
		before, _ := goal.Snapshot("reader")
		post, err := author.Post(t.Context(), finding(t, "new finding"))
		if err != nil {
			t.Fatal(err)
		}
		waitIdle(t, goal)
		after, _ := goal.Snapshot("reader")
		writer, _ := goal.Snapshot("author")
		if after.Activations != 2 || writer.Activations != 1 || after.RunID == before.RunID || after.Pending != 0 {
			t.Fatalf("reader=%+v author=%+v", after, writer)
		}
		assignments, notices := 0, 0
		for _, message := range after.Messages {
			if message.Content == "original assignment" {
				assignments++
			}
			if message.Observation.Version != 0 {
				notices++
				if strings.Contains(message.Content, "full evidence not injected") {
					t.Fatal("body pushed instead of metadata")
				}
			}
		}
		if assignments != 1 || notices != 1 {
			t.Fatalf("assignments=%d notices=%d", assignments, notices)
		}
		posts, err := author.Read(t.Context(), []board.ID{post.ID})
		if err != nil || posts[0].Body != "full evidence not injected" || posts[0].Author != "agent/author" {
			t.Fatalf("explicit pull = %+v, %v", posts, err)
		}
		after.Messages[0].Content = "caller change"
		again, _ := goal.Snapshot("reader")
		if again.Messages[0].Content == "caller change" {
			t.Fatal("snapshot aliases retained history")
		}
	})
}

func TestPostDuringSealedTurnWaitsForSettlementAndCoalesces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("handoff")
		sealed, settle := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		add(t, goal, "reader", func(ctx context.Context, a swarm.Activation) runner.TaskResult {
			result := runTurn(ctx, a)
			if calls.Add(1) == 1 {
				close(sealed)
				select {
				case <-settle:
				case <-ctx.Done():
				}
			}
			return result
		})
		launch(t, goal)
		<-sealed
		for range 5 {
			if _, err := goal.Post(t.Context(), finding(t, "arrived during save")); err != nil {
				t.Fatal(err)
			}
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("started another turn before settlement")
		}
		close(settle)
		waitIdle(t, goal)
		snapshot, _ := goal.Snapshot("reader")
		if snapshot.Activations != 2 || snapshot.Pending != 0 {
			t.Fatalf("coalesced activation = %+v", snapshot)
		}
	})
}

func TestAdmissionIsScopedExactAndIndependentOfReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("admission")
		ready, release := make(chan swarm.Activation, 1), make(chan struct{})
		member := add(t, goal, "reader", func(ctx context.Context, a swarm.Activation) runner.TaskResult {
			ready <- a
			select {
			case <-release:
			case <-ctx.Done():
			}
			return runTurn(ctx, a)
		})
		launch(t, goal)
		a := <-ready
		inputs := a.Inputs(nil)
		first, _ := goal.Post(t.Context(), finding(t, "first"))
		notice := inputs.Ready(a.Spec.ID)
		if len(notice.Inputs) != 1 || len(inputs.Ready(a.Spec.ID).Inputs) != 1 || inputs.Finish(a.Spec.ID) {
			t.Fatal("Ready consumed input or Finish sealed pending work")
		}
		_, _ = member.Read(t.Context(), []board.ID{first.ID})
		refs := []tools.AdmissionReference{notice.Inputs[0].Reference}
		if got := inputs.Admit(taskscope.ID("another-run"), refs); len(got) != 0 {
			t.Fatal("cross-run acknowledgment accepted")
		}
		_, _ = goal.Post(t.Context(), finding(t, "newer"))
		if got := inputs.Admit(a.Spec.ID, refs); len(got) != 1 {
			t.Fatal("offered reference not acknowledged")
		}
		if got := inputs.Admit(a.Spec.ID, refs); len(got) != 0 {
			t.Fatal("duplicate acknowledgment accepted")
		}
		if remaining := inputs.Ready(a.Spec.ID); len(remaining.Inputs) != 1 || !strings.Contains(remaining.Inputs[0].Message.Content, "newer") {
			t.Fatalf("newer message lost: %+v", remaining)
		}
		close(release)
		waitIdle(t, goal)
	})
}

func TestGoalIsolationLateJoinAndCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits := swarm.DefaultLimits()
		limits.Members = 1
		goal := swarm.New("a", swarm.WithLimits(limits))
		other := swarm.New("b")
		t.Cleanup(func() { _ = other.Close(t.Context()) })
		_, _ = goal.Post(t.Context(), finding(t, "retained before join"))
		member := add(t, goal, "reader", runTurn)
		foreign := add(t, other, "reader", runTurn)
		if _, err := foreign.Read(t.Context(), []board.ID{1}); !errors.Is(err, board.ErrNotFound) {
			t.Fatalf("cross-goal finding = %v", err)
		}
		if _, err := goal.Add(swarm.Agent{ID: "reader"}); !errors.Is(err, swarm.ErrConflict) {
			t.Fatalf("duplicate = %v", err)
		}
		if _, err := goal.Add(swarm.Agent{ID: "overflow"}); !errors.Is(err, swarm.ErrCapacity) {
			t.Fatalf("member capacity = %v", err)
		}
		launch(t, goal)
		waitIdle(t, goal)
		page, _ := member.List(t.Context(), 0, 1, "")
		snapshot, _ := goal.Snapshot("reader")
		if len(page.Posts) != 1 || snapshot.Pending != 0 {
			t.Fatalf("late join: %+v %+v", page, snapshot)
		}
	})
}

func TestPanicSuspendsMemberWithoutRetryAndShutdownJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("panic isolation")
		var executions atomic.Int32
		add(t, goal, "panics", func(context.Context, swarm.Activation) runner.TaskResult {
			executions.Add(1)
			panic("fixture panic")
		})
		launch(t, goal)
		waitIdle(t, goal)
		if _, err := goal.Post(t.Context(), finding(t, "must not restart a panicked member")); err != nil {
			t.Fatal(err)
		}
		waitIdle(t, goal)
		snapshot, err := goal.Snapshot("panics")
		if err != nil || snapshot.State != swarm.States.SUSPENDED || snapshot.RunID == "" || executions.Load() != 1 {
			t.Fatalf("panic settlement: %+v, executions=%d, err=%v", snapshot, executions.Load(), err)
		}
		if snapshot.LastError == nil || !strings.Contains(snapshot.LastError.Error(), "swarm execution panic: fixture panic") {
			t.Fatalf("panic diagnostic = %v", snapshot.LastError)
		}
		if err := goal.Close(t.Context()); err != nil {
			t.Fatalf("join panicked member: %v", err)
		}
	})
}

func TestFailureSuspendsAndShutdownJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("failure")
		failure := errors.New("settlement failed")
		add(t, goal, "failed", func(context.Context, swarm.Activation) runner.TaskResult {
			return runner.TaskResult{Reason: runner.TerminalError, Err: failure}
		})
		launch(t, goal)
		waitIdle(t, goal)
		_, _ = goal.Post(t.Context(), finding(t, "must not retry"))
		waitIdle(t, goal)
		snapshot, _ := goal.Snapshot("failed")
		if snapshot.Activations != 1 || snapshot.State != swarm.States.SUSPENDED || !errors.Is(snapshot.LastError, failure) {
			t.Fatalf("failure policy = %+v", snapshot)
		}
	})
}

func TestConcurrentPostsAndStopDoNotReactivateStoppedMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("stop")
		entered, drained := make(chan struct{}), make(chan struct{})
		add(t, goal, "reader", func(ctx context.Context, a swarm.Activation) runner.TaskResult {
			close(entered)
			<-ctx.Done()
			close(drained)
			return runner.TaskResult{Reason: runner.TerminalCancelled, Err: ctx.Err()}
		})
		launch(t, goal)
		<-entered
		draft := finding(t, "concurrent post")
		var writers sync.WaitGroup
		for range 16 {
			writers.Go(func() { _, _ = goal.Post(t.Context(), draft) })
		}
		if err := goal.Stop("reader"); err != nil {
			t.Fatal(err)
		}
		writers.Wait()
		waitIdle(t, goal)
		<-drained
		snapshot, _ := goal.Snapshot("reader")
		if snapshot.Activations != 1 || snapshot.State != swarm.States.STOPPED {
			t.Fatalf("stopped member = %+v", snapshot)
		}
	})
}

func TestActivationBudgetStopsFeedback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits := swarm.DefaultLimits()
		limits.Activations = 1
		goal := swarm.New("budget", swarm.WithLimits(limits))
		add(t, goal, "reader", runTurn)
		launch(t, goal)
		waitIdle(t, goal)
		_, _ = goal.Post(t.Context(), finding(t, "would activate again"))
		synctest.Wait()
		if _, err := goal.Post(t.Context(), finding(t, "after budget")); !errors.Is(err, swarm.ErrClosed) {
			t.Fatalf("budget did not close admission: %v", err)
		}
	})
}
