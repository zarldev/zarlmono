package runner_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type sinkContextKey struct{}

type callbackContextSink struct {
	observe func(context.Context, string, taskscope.ID)
}

func (s callbackContextSink) OnContent(ctx context.Context, e runner.Content) {
	s.observe(ctx, "Content", e.TaskID)
}

func (s callbackContextSink) OnThinking(ctx context.Context, e runner.Thinking) {
	s.observe(ctx, "Thinking", e.TaskID)
}

func (s callbackContextSink) OnToolStarted(ctx context.Context, e runner.ToolStarted) {
	s.observe(ctx, "ToolStarted", e.TaskID)
}

func (s callbackContextSink) OnToolCompleted(ctx context.Context, e runner.ToolCompleted) {
	s.observe(ctx, "ToolCompleted", e.TaskID)
}

func (s callbackContextSink) OnToolFailed(ctx context.Context, e runner.ToolFailed) {
	s.observe(ctx, "ToolFailed", e.TaskID)
}

func (s callbackContextSink) OnWorkspaceWaitStarted(ctx context.Context, e runner.WorkspaceWaitStarted) {
	s.observe(ctx, "WorkspaceWaitStarted", e.TaskID)
}

func (s callbackContextSink) OnWorkspaceWaitEnded(ctx context.Context, e runner.WorkspaceWaitEnded) {
	s.observe(ctx, "WorkspaceWaitEnded", e.TaskID)
}

func (s callbackContextSink) OnConversationStarted(ctx context.Context, e runner.ConversationStarted) {
	s.observe(ctx, "ConversationStarted", e.TaskID)
}

func (s callbackContextSink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	s.observe(ctx, "ConversationEnded", e.TaskID)
}

func (s callbackContextSink) OnIterationCompleted(ctx context.Context, e runner.IterationCompleted) {
	s.observe(ctx, "IterationCompleted", e.TaskID)
}

func (s callbackContextSink) OnProviderAttemptSettled(ctx context.Context, e runner.ProviderAttemptSettled) {
	s.observe(ctx, "ProviderAttemptSettled", e.TaskID)
}

func (s callbackContextSink) OnSteerInjected(ctx context.Context, e runner.SteerInjected) {
	s.observe(ctx, "SteerInjected", e.TaskID)
}

func (s callbackContextSink) OnCompactionApplied(ctx context.Context, e runner.CompactionApplied) {
	s.observe(ctx, "CompactionApplied", e.TaskID)
}

func (s callbackContextSink) OnDiagnostic(ctx context.Context, e runner.Diagnostic) {
	s.observe(ctx, "Diagnostic", e.TaskID)
}

func (s callbackContextSink) OnWaitingForInputs(ctx context.Context, e runner.WaitingForInputs) {
	s.observe(ctx, "WaitingForInputs", e.TaskID)
}

func (s callbackContextSink) OnInputsAdmitted(ctx context.Context, e runner.InputsAdmitted) {
	s.observe(ctx, "InputsAdmitted", e.TaskID)
}

func TestSyncSinkForwardsContextAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.WithValue(t.Context(), sinkContextKey{}, "trace"))
	cause := errors.New("caller stopped")
	cancel(cause)
	calls := []struct {
		name string
		send func(runner.EventSink, context.Context)
	}{
		{"Content", func(s runner.EventSink, ctx context.Context) { s.OnContent(ctx, runner.Content{TaskID: "task"}) }},
		{"Thinking", func(s runner.EventSink, ctx context.Context) { s.OnThinking(ctx, runner.Thinking{TaskID: "task"}) }},
		{"ToolStarted", func(s runner.EventSink, ctx context.Context) {
			s.OnToolStarted(ctx, runner.ToolStarted{TaskID: "task"})
		}},
		{"ToolCompleted", func(s runner.EventSink, ctx context.Context) {
			s.OnToolCompleted(ctx, runner.ToolCompleted{TaskID: "task"})
		}},
		{"ToolFailed", func(s runner.EventSink, ctx context.Context) { s.OnToolFailed(ctx, runner.ToolFailed{TaskID: "task"}) }},
		{"WorkspaceWaitStarted", func(s runner.EventSink, ctx context.Context) {
			s.OnWorkspaceWaitStarted(ctx, runner.WorkspaceWaitStarted{TaskID: "task"})
		}},
		{"WorkspaceWaitEnded", func(s runner.EventSink, ctx context.Context) {
			s.OnWorkspaceWaitEnded(ctx, runner.WorkspaceWaitEnded{TaskID: "task"})
		}},
		{"ConversationStarted", func(s runner.EventSink, ctx context.Context) {
			s.OnConversationStarted(ctx, runner.ConversationStarted{TaskID: "task"})
		}},
		{"ConversationEnded", func(s runner.EventSink, ctx context.Context) {
			s.OnConversationEnded(ctx, runner.ConversationEnded{TaskID: "task"})
		}},
		{"IterationCompleted", func(s runner.EventSink, ctx context.Context) {
			s.OnIterationCompleted(ctx, runner.IterationCompleted{TaskID: "task"})
		}},
		{"ProviderAttemptSettled", func(s runner.EventSink, ctx context.Context) {
			s.OnProviderAttemptSettled(ctx, runner.ProviderAttemptSettled{TaskID: "task"})
		}},
		{"SteerInjected", func(s runner.EventSink, ctx context.Context) {
			s.OnSteerInjected(ctx, runner.SteerInjected{TaskID: "task"})
		}},
		{"CompactionApplied", func(s runner.EventSink, ctx context.Context) {
			s.OnCompactionApplied(ctx, runner.CompactionApplied{TaskID: "task"})
		}},
		{"Diagnostic", func(s runner.EventSink, ctx context.Context) { s.OnDiagnostic(ctx, runner.Diagnostic{TaskID: "task"}) }},
		{"WaitingForInputs", func(s runner.EventSink, ctx context.Context) {
			s.OnWaitingForInputs(ctx, runner.WaitingForInputs{TaskID: "task"})
		}},
		{"InputsAdmitted", func(s runner.EventSink, ctx context.Context) {
			s.OnInputsAdmitted(ctx, runner.InputsAdmitted{TaskID: "task"})
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			sink := runner.NewSyncSink(callbackContextSink{observe: func(got context.Context, name string, id taskscope.ID) {
				called = true
				if got != ctx || name != tc.name || id != "task" || !errors.Is(context.Cause(got), cause) {
					t.Errorf("callback context/event changed: %s %s, cause %v", name, id, context.Cause(got))
				}
			}})
			tc.send(sink, ctx)
			if !called {
				t.Fatal("cancelled context suppressed event")
			}
		})
	}
}

func TestRunnerSinkContextsRemainScopedAcrossConcurrentRuns(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	counts := make(map[taskscope.ID]map[string]int)
	sink := runner.NewSyncSink(callbackContextSink{observe: func(ctx context.Context, event string, id taskscope.ID) {
		if ctx.Value(sinkContextKey{}) != string(id) || taskscope.IDFrom(ctx) != id {
			t.Errorf("%s for %s lost caller/task identity", event, id)
		}
		if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
			t.Errorf("%s lost caller deadline: %v", event, got)
		}
		if counts[id] == nil {
			counts[id] = make(map[string]int)
		}
		counts[id][event]++
	}})
	var wg sync.WaitGroup
	parent := t.Context()
	for _, id := range []taskscope.ID{"one", "two"} {
		wg.Go(func() {
			ctx, cancel := context.WithDeadline(context.WithValue(parent, sinkContextKey{}, string(id)), deadline)
			defer cancel()
			client := runnertest.NewClient([][]llm.CompletionChunk{
				{{Thinking: "thinking"}, runnertest.ChunkToolCall("a", "ok", "{}"), runnertest.ChunkToolCall("b", "fail", "{}")},
				{runnertest.ChunkText("done")},
			})
			reg := tools.NewRegistry(runnertest.Tool{Name: "ok", Result: "ok"}, runnertest.Tool{Name: "fail", Err: errors.New("tool error")})
			r := runner.New(client, runner.WithTools(reg), runner.WithSink(sink), runner.WithToolConcurrency(2))
			if result := r.Run(ctx, runner.TaskSpec{ID: id, Prompt: "go"}); result.Err != nil {
				t.Errorf("run %s: %v", id, result.Err)
			}
		})
	}
	wg.Wait()
	for _, id := range []taskscope.ID{"one", "two"} {
		for _, event := range []string{"ConversationStarted", "Thinking", "Content", "ToolStarted", "ToolCompleted", "ToolFailed", "ProviderAttemptSettled", "IterationCompleted", "ConversationEnded"} {
			if counts[id][event] == 0 {
				t.Errorf("%s missing %s", id, event)
			}
		}
	}
}

func TestRunnerSinkRetainsCancellationCauseAtSettlement(t *testing.T) {
	cause := errors.New("user cancelled turn")
	ctx, cancel := context.WithCancelCause(context.WithValue(t.Context(), sinkContextKey{}, "trace"))
	defer cancel(nil)
	settled, ended := false, false
	sink := callbackContextSink{observe: func(got context.Context, event string, _ taskscope.ID) {
		if got.Value(sinkContextKey{}) != "trace" {
			t.Errorf("%s lost trace value", event)
		}
		if event == "Content" {
			cancel(cause)
		}
		if event == "ProviderAttemptSettled" || event == "ConversationEnded" {
			if !errors.Is(context.Cause(got), cause) {
				t.Errorf("%s cause = %v", event, context.Cause(got))
			}
			settled = settled || event == "ProviderAttemptSettled"
			ended = ended || event == "ConversationEnded"
		}
	}}
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("partial")}})
	result := runner.New(client, runner.WithSink(sink)).Run(ctx, runner.TaskSpec{ID: "cancelled", Prompt: "go"})
	if !errors.Is(result.Err, cause) || !settled || !ended {
		t.Fatalf("settlement=%v end=%v result=%v", settled, ended, result.Err)
	}
}

func TestRunnerSetupFailurePublishesCallerContext(t *testing.T) {
	ctx := context.WithValue(t.Context(), sinkContextKey{}, "setup")
	count := 0
	sink := callbackContextSink{observe: func(got context.Context, event string, _ taskscope.ID) {
		count++
		if got != ctx {
			t.Errorf("%s lost setup context", event)
		}
	}}
	result := runner.New(runnertest.NewClient(nil), runner.WithSink(sink)).Run(ctx, runner.TaskSpec{ID: "invalid", MaxIterations: -1})
	if !errors.Is(result.Err, runner.ErrInvalidIterations) || count != 2 {
		t.Fatalf("events=%d result=%v", count, result.Err)
	}
}

func TestRunnerNestedAndWorkspaceEventsPreserveExecutionContext(t *testing.T) {
	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.Acquire("holder", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	counts := make(map[string]int)
	sink := callbackContextSink{observe: func(ctx context.Context, event string, _ taskscope.ID) {
		if ctx.Value(sinkContextKey{}) == "nested" {
			counts[event]++
		}
		if event == "WorkspaceWaitStarted" {
			holder.Release()
		}
	}}
	composite := tools.New(tools.ToolSpec{Name: "composite", Description: "nested context fixture"}, func(ctx context.Context, _ map[string]any) (string, error) {
		ctx = context.WithValue(ctx, sinkContextKey{}, "nested")
		call := tools.NestedToolCall{ParentID: "outer", ChildID: "inner", Call: tools.ToolCall{ID: "inner", ToolName: "read"}}
		observer := tools.NestedToolObserverFromContext(ctx)
		observer.OnNestedToolStarted(ctx, call)
		ctx = tools.ContextWithWorkspaceWaitCall(ctx, tools.WorkspaceWaitCall{ParentToolID: "outer", ToolID: "inner", ToolName: "read"})
		lease, err := coordinator.AcquirePathsWait(ctx, "reader", tools.WorkspaceAccesses.READ, nil)
		if err != nil {
			return "", err
		}
		defer lease.Release()
		observer.OnNestedToolFinished(ctx, tools.NestedToolResult{NestedToolCall: call, Result: tools.Success("inner", "read")})
		return "done", nil
	})
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("outer", "composite", "{}")}, {runnertest.ChunkText("done")}})
	r := runner.New(client, runner.WithTools(tools.NewRegistry(composite)), runner.WithSink(sink))
	if result := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"}); result.Err != nil {
		t.Fatal(result.Err)
	}
	for _, event := range []string{"ToolStarted", "ToolCompleted", "WorkspaceWaitStarted", "WorkspaceWaitEnded"} {
		if counts[event] != 1 {
			t.Errorf("nested context: %s count = %d; want 1", event, counts[event])
		}
	}
}
