package runner_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestAutomaticInputsWaitWithoutProviderAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		group, child, release := inputFixture(t, ctx)
		provider := &fakeProvider{turns: [][]llm.CompletionChunk{{chunkText("provisional")}, {chunkText("considered child")}}}
		sink := &inputEvents{waiting: make(chan struct{}, 1)}
		parent := runner.New(runner.ClientFromProvider(provider), runner.WithInputSource(group), runner.WithSink(sink), runner.WithMaxIterations(2))
		var wg sync.WaitGroup
		var result runner.TaskResult
		wg.Go(func() { result = parent.Run(ctx, runner.TaskSpec{ID: "parent", Prompt: "original assignment"}) })
		defer func() { cancel(); _ = group.Close(context.WithoutCancel(ctx)); wg.Wait() }()
		<-sink.waiting
		synctest.Wait()
		if calls := provider.callCount(); calls != 1 {
			t.Fatalf("provider calls while waiting = %d", calls)
		}
		close(release)
		wg.Wait()
		if result.Reason != runner.TerminalCompleted || result.Iterations != 2 {
			t.Fatalf("result = %#v", result)
		}
		if got := countObservations(provider.request(1)); got != 1 {
			t.Fatalf("resumed request observations = %d", got)
		}
		if ready := group.Ready("parent"); ready.Outstanding || len(ready.Inputs) != 0 {
			t.Fatalf("admitted result remains ready: %#v", ready)
		}
		if snapshot, err := group.Peek(child); err != nil || !snapshot.Admitted {
			t.Fatalf("admitted snapshot = %#v, %v", snapshot, err)
		}
		if len(sink.admitted) != 1 || len(sink.admitted[0].Messages) != 1 {
			t.Fatalf("admission events = %#v", sink.admitted)
		}
	})
}

func TestAutomaticInputsExplicitAwaitReconcilesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		group, child, release := inputFixture(t, ctx)
		reg := tools.NewRegistry()
		if err := reg.Register(spawn.NewAwait(group)); err != nil {
			t.Fatal(err)
		}
		provider := &fakeProvider{turns: [][]llm.CompletionChunk{
			{chunkToolCall("await", "agent_await", fmt.Sprintf(`{"task_id":%q}`, child))},
			{chunkText("considered explicit result")},
		}}
		sink := &inputEvents{waiting: make(chan struct{}, 1)}
		parent := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithInputSource(group), runner.WithSink(sink), runner.WithMaxIterations(2))
		var wg sync.WaitGroup
		var result runner.TaskResult
		wg.Go(func() { result = parent.Run(ctx, runner.TaskSpec{ID: "parent", Prompt: "work"}) })
		defer func() { cancel(); _ = group.Close(context.WithoutCancel(ctx)); wg.Wait() }()
		synctest.Wait()
		close(release)
		wg.Wait()
		if result.Reason != runner.TerminalCompleted || countObservations(provider.request(1)) != 0 {
			t.Fatalf("explicit result was automatically repeated: %#v", result)
		}
		if len(sink.admitted) != 1 || len(sink.admitted[0].Messages) != 0 || len(sink.admitted[0].References) != 1 {
			t.Fatalf("explicit admission events = %#v", sink.admitted)
		}
	})
}

func TestAutomaticInputsRespectBudgetAndCancellation(t *testing.T) {
	for _, budget := range []int{1, 2} {
		t.Run(strconv.Itoa(budget), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				group, _, _ := inputFixture(t, ctx)
				provider := &fakeProvider{turns: [][]llm.CompletionChunk{{chunkText("provisional")}}}
				sink := &inputEvents{waiting: make(chan struct{}, 1)}
				parent := runner.New(runner.ClientFromProvider(provider), runner.WithInputSource(group), runner.WithSink(sink), runner.WithMaxIterations(budget))
				var wg sync.WaitGroup
				var result runner.TaskResult
				wg.Go(func() { result = parent.Run(ctx, runner.TaskSpec{ID: "parent", Prompt: "work"}) })
				defer func() { cancel(); _ = group.Close(context.WithoutCancel(ctx)); wg.Wait() }()
				want := runner.TerminalMaxIterations
				if budget == 2 {
					<-sink.waiting
					cancel()
					want = runner.TerminalCancelled
				}
				wg.Wait()
				if result.Reason != want || provider.callCount() != 1 {
					t.Fatalf("budget/cancellation result = %#v, calls = %d", result, provider.callCount())
				}
			})
		})
	}
}

func TestAutomaticInputsFailedHistoryDoesNotAcknowledge(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	group, child, release := inputFixture(t, ctx)
	defer func() { _ = group.Close(context.WithoutCancel(ctx)) }()
	close(release)
	if _, err := group.Wait(ctx, child); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{}
	parent := runner.New(runner.ClientFromProvider(provider), runner.WithInputSource(group), runner.WithHistorySink(rejectObservationHistory{}))
	result := parent.Run(ctx, runner.TaskSpec{ID: "parent", Prompt: "work"})
	if !errors.Is(result.Err, runner.ErrReplayHistory) || provider.callCount() != 0 {
		t.Fatalf("failed capture dispatched: %#v", result)
	}
	if ready := group.Ready("parent"); len(ready.Inputs) != 1 || !ready.Outstanding {
		t.Fatalf("failed capture consumed result: %#v", ready)
	}
}

func TestAutomaticInputsFinishDoesNotDuplicateIteration(t *testing.T) {
	source := &settlingInputSource{}
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{{chunkText("done")}}}
	sink := &inputEvents{}
	parent := runner.New(runner.ClientFromProvider(provider), runner.WithInputSource(source), runner.WithSink(sink), runner.WithMaxIterations(2))

	result := parent.Run(t.Context(), runner.TaskSpec{ID: "parent", Prompt: "work"})
	if result.Reason != runner.TerminalCompleted || result.Iterations != 1 || provider.callCount() != 1 {
		t.Fatalf("result = %#v, provider calls = %d", result, provider.callCount())
	}
	if source.finishes != 2 {
		t.Fatalf("finish calls = %d", source.finishes)
	}
	if sink.iterations != 1 {
		t.Fatalf("iteration events = %d", sink.iterations)
	}
}

func TestAutomaticInputsLossyToolResultDoesNotAcknowledge(t *testing.T) {
	reference := tools.AdmissionReference{Namespace: "test", ID: "result"}
	source := &recordingInputSource{}
	reg := newRegistry(admissionTool{reference: reference})
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("call", "admission", `{}`)},
		{chunkText("done")},
	}}
	parent := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithInputSource(source),
		runner.WithResultTruncator(runner.DefaultTruncator{MaxBytes: 8, MaxLines: 1}), runner.WithMaxIterations(2))

	result := parent.Run(t.Context(), runner.TaskSpec{ID: "parent", Prompt: "work"})
	if result.Reason != runner.TerminalCompleted {
		t.Fatalf("result = %#v", result)
	}
	if len(source.admitted) != 0 {
		t.Fatalf("lossy result acknowledged references: %#v", source.admitted)
	}
	request := provider.request(1)
	for _, message := range request.Messages {
		if message.Role == llm.RoleTool && !strings.Contains(message.Content, "truncated") {
			t.Fatalf("tool result was not truncated: %q", message.Content)
		}
	}
}

type settlingInputSource struct {
	finishes int
}

func (*settlingInputSource) Ready(taskscope.ID) runner.ReadyInputs { return runner.ReadyInputs{} }
func (*settlingInputSource) Admit(taskscope.ID, []tools.AdmissionReference) []tools.AdmissionReference {
	return nil
}
func (s *settlingInputSource) Finish(taskscope.ID) bool {
	s.finishes++
	return s.finishes > 1
}

type recordingInputSource struct {
	admitted []tools.AdmissionReference
}

func (*recordingInputSource) Ready(taskscope.ID) runner.ReadyInputs { return runner.ReadyInputs{} }
func (s *recordingInputSource) Admit(_ taskscope.ID, references []tools.AdmissionReference) []tools.AdmissionReference {
	s.admitted = append(s.admitted, references...)
	return references
}
func (*recordingInputSource) Finish(taskscope.ID) bool { return true }

type admissionTool struct {
	reference tools.AdmissionReference
}

func (admissionTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "admission", Description: "returns an admission reference"}
}

func (t admissionTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "full output that cannot be recovered after trimming",
		AdmissionReferences: []tools.AdmissionReference{t.reference}}, nil
}

func inputFixture(t *testing.T, ctx context.Context) (*spawn.Group, spawn.TaskID, chan struct{}) {
	t.Helper()
	group := spawn.NewGroup()
	if _, err := group.Bind(ctx, "parent"); err != nil {
		t.Fatal(err)
	}
	client := inputChildClient{started: make(chan struct{}), release: make(chan struct{})}
	tool := spawn.NewAsync(runner.New(client), group)
	result, err := tool.Execute(taskscope.WithID(ctx, "parent"), tools.ToolCall{ID: "spawn", ExecutionID: "original-assignment", Arguments: tools.ToolParameters{"prompt": "child work"}})
	if err != nil || !result.Success {
		t.Fatalf("spawn = %#v, %v", result, err)
	}
	<-client.started
	return group, spawn.TaskID(result.Data.(map[string]any)["task_id"].(string)), client.release
}

type inputChildClient struct {
	started chan struct{}
	release chan struct{}
}

func (c inputChildClient) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(c.started)
		select {
		case <-ctx.Done():
			yield(llm.CompletionChunk{}, ctx.Err())
		case <-c.release:
			yield(llm.CompletionChunk{Content: "reported evidence"}, nil)
		}
	}
}

func countObservations(request llm.CompletionRequest) int {
	count := 0
	for _, message := range request.Messages {
		if message.Observation.Version != 0 {
			count++
		}
	}
	return count
}

type inputEvents struct {
	runner.NopSink
	waiting    chan struct{}
	admitted   []runner.InputsAdmitted
	iterations int
}

func (s *inputEvents) OnWaitingForInputs(_ context.Context, event runner.WaitingForInputs) {
	if event.Waiting && s.waiting != nil {
		s.waiting <- struct{}{}
	}
}
func (s *inputEvents) OnInputsAdmitted(_ context.Context, event runner.InputsAdmitted) {
	s.admitted = append(s.admitted, event)
}

func (s *inputEvents) OnIterationCompleted(context.Context, runner.IterationCompleted) {
	s.iterations++
}

type rejectObservationHistory struct{}

func (rejectObservationHistory) Append(_ context.Context, records []runner.ReplayMessage) error {
	for _, record := range records {
		if record.Message.Observation.Version != 0 {
			return errors.New("history rejected observation")
		}
	}
	return nil
}

func (rejectObservationHistory) Request(context.Context, llm.CompletionRequest) error { return nil }
