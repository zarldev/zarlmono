package swarm_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestBoardAndChildCompletionWakeOneParkedExecution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("children and peers")
		child := blockedChild{release: make(chan struct{})}
		waiting := make(chan struct{}, 3)
		client := runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkText("provisional")},
			{runnertest.ChunkText("considered peer")},
			{runnertest.ChunkText("considered child")},
		})
		add(t, goal, "reader", func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
			group := spawn.NewGroup()
			defer func() { _ = group.Close(context.WithoutCancel(ctx)) }()
			if _, err := group.Bind(ctx, activation.Spec.ID); err != nil {
				return runner.TaskResult{Reason: runner.TerminalError, Err: err}
			}
			tool := spawn.NewAsync(runner.New(child), group)
			_, err := tool.Execute(taskscope.WithID(ctx, activation.Spec.ID), tools.ToolCall{
				ID: "child", ExecutionID: "child-execution", Arguments: tools.ToolParameters{"prompt": "child work"}})
			if err != nil {
				return runner.TaskResult{Reason: runner.TerminalError, Err: err}
			}
			inputs := activation.Inputs(group)
			r := runner.New(client, runner.WithInputSource(inputs), runner.WithSink(waitSink{waiting: waiting}))
			return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult { return r.Run(ctx, activation.Spec) })
		})
		launch(t, goal)
		<-waiting
		if _, err := goal.Post(t.Context(), finding(t, "peer while child running")); err != nil {
			t.Fatal(err)
		}
		<-waiting
		close(child.release)
		waitIdle(t, goal)
		snapshot, _ := goal.Snapshot("reader")
		if snapshot.Activations != 1 || snapshot.State != swarm.States.IDLE || client.CallCount() != 3 {
			t.Fatalf("wake path = %+v, calls=%d", snapshot, client.CallCount())
		}
		boardNotices, childNotices := 0, 0
		for _, message := range snapshot.Messages {
			if message.Observation.Version == 0 {
				continue
			}
			if strings.Contains(message.Content, "board_notice") {
				boardNotices++
			} else if strings.Contains(message.Content, "agent_completion") {
				childNotices++
			}
		}
		if boardNotices != 1 || childNotices != 1 {
			t.Fatalf("board=%d child=%d", boardNotices, childNotices)
		}
	})
}

func TestRejectedHistoryKeepsNotificationPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		goal := swarm.New("history failure")
		_, _ = goal.Post(t.Context(), finding(t, "must retain"))
		add(t, goal, "reader", func(ctx context.Context, activation swarm.Activation) runner.TaskResult {
			inputs := activation.Inputs(nil)
			r := runner.New(runnertest.NewClient(nil), runner.WithInputSource(inputs), runner.WithHistorySink(rejectPeerHistory{}))
			return inputs.Run(ctx, func(ctx context.Context) runner.TaskResult { return r.Run(ctx, activation.Spec) })
		})
		launch(t, goal)
		waitIdle(t, goal)
		snapshot, _ := goal.Snapshot("reader")
		if snapshot.Pending != 1 || snapshot.State != swarm.States.SUSPENDED || !errors.Is(snapshot.LastError, runner.ErrReplayHistory) {
			t.Fatalf("lost uncommitted observation: %+v", snapshot)
		}
	})
}

type blockedChild struct{ release chan struct{} }

func (c blockedChild) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		select {
		case <-c.release:
			yield(runnertest.ChunkText("child evidence"), nil)
		case <-ctx.Done():
			yield(llm.CompletionChunk{}, ctx.Err())
		}
	}
}

type waitSink struct {
	runner.NopSink
	waiting chan struct{}
}

func (s waitSink) OnWaitingForInputs(ctx context.Context, event runner.WaitingForInputs) {
	if event.Waiting && event.Depth == 0 {
		select {
		case s.waiting <- struct{}{}:
		case <-ctx.Done():
		}
	}
}

type rejectPeerHistory struct{}

func (rejectPeerHistory) Append(_ context.Context, records []runner.ReplayMessage) error {
	for _, record := range records {
		if record.Message.Observation.Version != 0 {
			return errors.New("history unavailable")
		}
	}
	return nil
}

func (rejectPeerHistory) Request(context.Context, llm.CompletionRequest) error { return nil }
