package swarm_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestChildRelaySubscribesBeforeExecutionStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("child startup ordering")
		child := &pausingChildSource{
			changed: make(chan struct{}), outstanding: true, pause: true,
			sampling: make(chan struct{}), resume: make(chan struct{}),
		}
		entered := make(chan struct{})
		add(t, goal, "reader", func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
			inputs := activation.Inputs(child)
			return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult {
				close(entered)
				client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("settled")}})
				return runner.New(client, runner.WithInputSource(inputs)).Run(ctx, activation.Spec)
			})
		})
		launch(t, goal)
		<-child.sampling
		synctest.Wait()
		select {
		case <-entered:
			t.Error("execution started before its child wake subscription was established")
		default:
		}
		child.complete()
		close(child.resume)
		waitIdle(t, goal)
		snapshot, err := goal.Snapshot("reader")
		if err != nil || snapshot.State != swarm.States.IDLE || snapshot.LastError != nil {
			t.Fatalf("startup completion did not settle: %+v, %v", snapshot, err)
		}
	})
}

func TestChildRelayRearmsBeforeWakingConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("child wake ordering")
		child := &pausingChildSource{
			changed: make(chan struct{}), outstanding: true,
			sampling: make(chan struct{}), resume: make(chan struct{}),
		}
		add(t, goal, "reader", func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
			inputs := activation.Inputs(child)
			return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult {
				ready := inputs.Ready(activation.Spec.ID)
				// Let the relay subscribe, then hold its next child snapshot. No
				// other consumer samples while this controlled transition is open.
				synctest.Wait()
				child.pauseAndSignal()
				<-child.sampling
				select {
				case <-ready.Changed:
					t.Error("consumer woke before the relay renewed its child subscription")
				default:
				}
				// Completion during re-subscription must be covered by the wake
				// published afterwards, not lost behind a fresh child token.
				child.complete()
				close(child.resume)
				select {
				case <-ready.Changed:
				case <-ctx.Done():
					return runner.TaskResult{Reason: runner.TerminalError, Err: ctx.Err()}
				}
				client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("settled")}})
				return runner.New(client, runner.WithInputSource(inputs)).Run(ctx, activation.Spec)
			})
		})
		launch(t, goal)
		waitIdle(t, goal)
		snapshot, err := goal.Snapshot("reader")
		if err != nil || snapshot.State != swarm.States.IDLE || snapshot.LastError != nil {
			t.Fatalf("child completion did not settle: %+v, %v", snapshot, err)
		}
	})
}

func TestChildRelayCancellationDrainsWithoutChildWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("cancel parked child receiver")
		child := &pausingChildSource{outstanding: true} // nil wake token: cancellation is the only exit.
		waiting, finished := make(chan struct{}, 1), make(chan struct{})
		add(t, goal, "reader", func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
			defer close(finished)
			inputs := activation.Inputs(child)
			client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("provisional")}})
			r := runner.New(client, runner.WithInputSource(inputs), runner.WithSink(waitSink{waiting: waiting}))
			return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult { return r.Run(ctx, activation.Spec) })
		})
		launch(t, goal)
		<-waiting
		if err := goal.Stop("reader"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Fatal("cancellation did not drain the execution and its child relay")
		}
	})
}

// pausingChildSource represents work that settles without an observation. Its
// gate pauses one readiness snapshot before it acquires the current wake token,
// making the completion/resubscription interleaving deterministic.
type pausingChildSource struct {
	mu          sync.Mutex
	changed     chan struct{}
	outstanding bool
	pause       bool
	sampling    chan struct{}
	resume      chan struct{}
}

func (s *pausingChildSource) Ready(taskscope.ID) runner.ReadyInputs {
	s.mu.Lock()
	pause := s.pause
	s.pause = false
	s.mu.Unlock()
	if pause {
		close(s.sampling)
		<-s.resume
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return runner.ReadyInputs{Outstanding: s.outstanding, Changed: s.changed}
}

func (*pausingChildSource) Admit(taskscope.ID, []tools.AdmissionReference) []tools.AdmissionReference {
	return nil
}

func (s *pausingChildSource) Finish(taskscope.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.outstanding
}

func (s *pausingChildSource) pauseAndSignal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pause = true
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *pausingChildSource) complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outstanding = false
	close(s.changed)
	s.changed = make(chan struct{})
}
