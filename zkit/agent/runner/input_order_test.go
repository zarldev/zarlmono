package runner_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestAutomaticInputsWaitBeforeCompletionCorrections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option options.Option[runner.Runner]
	}{
		{"quality", runner.WithTurnQuality(correctInputOnce{})},
		{"completion", runner.WithCompletionGate(runner.RequireWork{MaxCorrections: 1})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				group, _, release := inputFixture(t, ctx)
				provider := &fakeProvider{turns: [][]llm.CompletionChunk{
					{chunkText("provisional")}, {chunkText("considered child")}, {chunkText("done")},
				}}
				sink := &inputEvents{waiting: make(chan struct{}, 1)}
				parent := runner.New(runner.ClientFromProvider(provider), runner.WithInputSource(group), runner.WithSink(sink), runner.WithMaxIterations(3), tc.option)
				var wg sync.WaitGroup
				var result runner.TaskResult
				wg.Go(func() { result = parent.Run(ctx, runner.TaskSpec{ID: "parent", Prompt: "work"}) })
				defer func() { cancel(); _ = group.Close(context.WithoutCancel(ctx)); wg.Wait() }()
				<-sink.waiting
				synctest.Wait()
				if got := provider.callCount(); got != 1 {
					t.Fatalf("correction polled while waiting: %d requests", got)
				}
				close(release)
				wg.Wait()
				if result.Reason != runner.TerminalCompleted || result.Iterations != 3 || countObservations(provider.request(1)) != 1 {
					t.Fatalf("completion after child and correction = %#v", result)
				}
			})
		})
	}
}

func TestAutomaticInputsCorrectionDoesNotSealParent(t *testing.T) {
	group := spawn.NewGroup()
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	if _, err := group.Bind(t.Context(), "parent"); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	child := &fakeProvider{turns: [][]llm.CompletionChunk{{chunkText("child evidence")}}}
	if err := reg.Register(spawn.NewAsync(runner.New(runner.ClientFromProvider(child)), group)); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{chunkText("premature")},
		{chunkToolCall("spawn", "agent_spawn", `{"prompt":"additional work"}`)},
		{chunkText("provisional")},
		{chunkText("done")},
	}}
	parent := runner.New(runner.ClientFromProvider(provider), runner.WithInputSource(group), runner.WithTools(reg), runner.WithTurnQuality(correctInputOnce{}), runner.WithMaxIterations(4))
	result := parent.Run(t.Context(), runner.TaskSpec{ID: "parent", Prompt: "work"})
	if result.Reason != runner.TerminalCompleted {
		t.Fatalf("result = %#v", result)
	}
	tasks := group.List()
	if len(tasks) != 1 || !tasks[0].Admitted || tasks[0].State != spawn.AgentTaskStates.COMPLETED {
		t.Fatalf("corrective turn could not spawn and receive child: %#v", tasks)
	}
	if ready := group.Ready(taskscope.ID("parent")); ready.Outstanding {
		t.Fatal("finished scope retains outstanding work")
	}
}

type correctInputOnce struct{}

func (correctInputOnce) Inspect(string, []llm.ToolCall) runner.TurnQualityDecision {
	return runner.TurnQualityDecision{Correction: "review once more", MaxCorrections: 1}
}
