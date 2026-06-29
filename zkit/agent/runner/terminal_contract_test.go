package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func (s *recordingSink) endedEvents() []runner.ConversationEnded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]runner.ConversationEnded(nil), s.convEnded...)
}

func (s *recordingSink) startedEvents() []runner.ConversationStarted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]runner.ConversationStarted(nil), s.convStarts...)
}

// TestRun_SetupErrorEndsWithError covers P0.1: a failure rendering the
// system prompt happens before the main loop, so it must still emit a
// terminal ConversationEnded(Reason=error) — and NOT ConversationStarted.
// Otherwise a consumer that reacts to the event stream — zarlcode's
// conversation wrapper, which discards the returned error — sees the turn
// vanish silently.
func TestRun_SetupErrorEndsWithError(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{} // never reached: setup fails first
	sink := newRecordingSink()
	failing := runner.PromptFunc(func(context.Context, runner.PromptVars) (string, error) {
		return "", errors.New("prompt render boom")
	})
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
		runner.WithSink(sink), runner.WithPrompt(failing), runner.WithMaxIterations(3))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "hi",
	})
	if res.Err == nil {
		t.Fatal("expected a setup error from Run")
	}
	if res.Reason != runner.TerminalError {
		t.Errorf("reason = %v, want %v", res.Reason, runner.TerminalError)
	}
	ended := sink.endedEvents()
	if len(ended) != 1 {
		t.Fatalf("ConversationEnded events = %d, want 1", len(ended))
	}
	if ended[0].Reason != runner.TerminalError {
		t.Errorf("ended reason = %v, want error", ended[0].Reason)
	}
	if ended[0].Error == "" {
		t.Error("an error-terminal event carried no error text")
	}
	// Started must still fire (paired with Ended) so a consumer counting
	// in-flight runs stays balanced even on the setup-error path.
	if got := sink.startedEvents(); len(got) != 1 {
		t.Errorf("setup failure should still publish a paired ConversationStarted, got %d", len(got))
	}
	if provider.callCount() != 0 {
		t.Errorf("provider should never be called on setup failure, got %d", provider.callCount())
	}
}

// TestRun_InvalidIterationsEndsWithError covers the bookend invariant for
// the earliest terminal path: a negative MaxIterations is rejected before
// the loop, but it must STILL publish a paired ConversationStarted +
// ConversationEnded(error) so an event-driven consumer doesn't see the
// turn vanish. (The validation guard predates publishSetupFailed and used
// to return silently.)
func TestRun_InvalidIterationsEndsWithError(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{} // never reached: validation fails first
	sink := newRecordingSink()
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
		runner.WithSink(sink))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:            taskscope.ID(uuid.NewString()),
		Prompt:        "hi",
		MaxIterations: -1,
	})
	if !errors.Is(res.Err, runner.ErrInvalidIterations) {
		t.Fatalf("res.Err = %v, want ErrInvalidIterations", res.Err)
	}
	if res.Reason != runner.TerminalError {
		t.Errorf("reason = %v, want %v", res.Reason, runner.TerminalError)
	}
	if got := sink.startedEvents(); len(got) != 1 {
		t.Errorf("invalid-iterations should publish exactly one ConversationStarted, got %d", len(got))
	}
	ended := sink.endedEvents()
	if len(ended) != 1 {
		t.Fatalf("ConversationEnded events = %d, want 1", len(ended))
	}
	if ended[0].Reason != runner.TerminalError {
		t.Errorf("ended reason = %v, want error", ended[0].Reason)
	}
	if ended[0].Error == "" {
		t.Error("an error-terminal event carried no error text")
	}
	if provider.callCount() != 0 {
		t.Errorf("provider should never be called on invalid iterations, got %d", provider.callCount())
	}
}

// TestRun_ConversationEndedCarriesReason covers P0.2: the single terminal
// ConversationEnded event must carry the terminal reason so the
// event-driven consumer can tell a finished answer from a turn that hit
// the iteration cap.
func TestRun_ConversationEndedCarriesReason(t *testing.T) {
	t.Parallel()

	t.Run("normal completion", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{turns: [][]llm.CompletionChunk{
			{chunkText("the answer"), chunkDone()},
		}}
		sink := newRecordingSink()
		r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
			runner.WithSink(sink), runner.WithMaxIterations(3))
		if res := r.Run(t.Context(), runner.TaskSpec{
			ID: taskscope.ID(uuid.NewString()), Prompt: "q",
		}); res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		ev := sink.endedEvents()
		if len(ev) != 1 {
			t.Fatalf("ended events = %d, want 1", len(ev))
		}
		if ev[0].Reason != runner.TerminalCompleted {
			t.Errorf("reason = %v, want %v", ev[0].Reason, runner.TerminalCompleted)
		}
	})

	t.Run("max iterations", func(t *testing.T) {
		t.Parallel()
		turns := make([][]llm.CompletionChunk, 5)
		for i := range turns {
			turns[i] = []llm.CompletionChunk{chunkToolCall("loop", "echo", `{}`), chunkDone()}
		}
		provider := &fakeProvider{turns: turns}
		sink := newRecordingSink()
		r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry(stubTool{name: "echo"})),
			runner.WithSink(sink), runner.WithMaxIterations(3))
		if res := r.Run(t.Context(), runner.TaskSpec{
			ID: taskscope.ID(uuid.NewString()), Prompt: "loop",
		}); res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		ev := sink.endedEvents()
		if len(ev) != 1 {
			t.Fatalf("ended events = %d, want 1", len(ev))
		}
		if ev[0].Reason != runner.TerminalMaxIterations {
			t.Errorf("reason = %v, want %v", ev[0].Reason, runner.TerminalMaxIterations)
		}
	})
}

// TestRun_SystemPromptInResult covers the self-contained-transcript
// contract: TaskResult.SystemPrompt carries the prompt a run used so
// post-hoc debugging needs no external state, while Messages still
// omits the system message (stripSystem) so REPL callers don't double
// it when feeding history back as Context.
func TestRun_SystemPromptInResult(t *testing.T) {
	t.Parallel()

	const sysText = "you are a focused test agent"

	t.Run("with prompt source", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{
			turns: [][]llm.CompletionChunk{{chunkText("ok"), chunkDone()}},
		}
		prompt := runner.PromptFunc(func(context.Context, runner.PromptVars) (string, error) {
			return sysText, nil
		})
		r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
			runner.WithPrompt(prompt), runner.WithMaxIterations(3))
		res := r.Run(t.Context(), runner.TaskSpec{
			ID: taskscope.ID(uuid.NewString()), Prompt: "hi",
		})
		if res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		if res.SystemPrompt != sysText {
			t.Errorf("SystemPrompt = %q, want %q", res.SystemPrompt, sysText)
		}
		for _, m := range res.Messages {
			if m.Role == llm.RoleSystem {
				t.Errorf("Messages must exclude the system message, found %q", m.Content)
			}
		}
	})

	t.Run("without prompt source", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{
			turns: [][]llm.CompletionChunk{{chunkText("ok"), chunkDone()}},
		}
		r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
			runner.WithMaxIterations(3))
		res := r.Run(t.Context(), runner.TaskSpec{
			ID: taskscope.ID(uuid.NewString()), Prompt: "hi",
		})
		if res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		if res.SystemPrompt != "" {
			t.Errorf("SystemPrompt = %q, want empty (no prompt source)", res.SystemPrompt)
		}
	})

	t.Run("survives mid-run compaction", func(t *testing.T) {
		t.Parallel()
		// Two iterations: a tool call on iter 0, completion on iter 1.
		// The compactor fires between them with keep=1, trimming history
		// to system + tail — SystemPrompt must still be the original.
		provider := &fakeProvider{
			turns: [][]llm.CompletionChunk{
				{chunkToolCall("c1", "echo", `{}`), chunkDone()},
				{chunkText("done"), chunkDone()},
			},
		}
		prompt := runner.PromptFunc(func(context.Context, runner.PromptVars) (string, error) {
			return sysText, nil
		})
		c := &trackingCompactor{keep: 1}
		r := runner.New(runner.ClientFromProvider(provider),
			runner.WithTools(newRegistry(stubTool{name: "echo"})),
			runner.WithPrompt(prompt), runner.WithCompactor(c), runner.WithMaxIterations(5))
		res := r.Run(t.Context(), runner.TaskSpec{
			ID: taskscope.ID(uuid.NewString()), Prompt: "hi",
		})
		if res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		if c.calls.Load() == 0 {
			t.Fatal("compactor never fired — test does not exercise the mid-run path")
		}
		if res.SystemPrompt != sysText {
			t.Errorf("SystemPrompt after compaction = %q, want %q", res.SystemPrompt, sysText)
		}
	})
}
