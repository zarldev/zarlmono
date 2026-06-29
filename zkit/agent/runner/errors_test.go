package runner_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// errPromptSource always returns a fixed error so we can assert
// the sentinel wrap.
type errPromptSource struct{ err error }

func (e errPromptSource) System(_ context.Context, _ runner.PromptVars) (string, error) {
	return "", e.err
}

func TestErr_PromptRender_IsErrPromptRender(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{turns: nil}
	reg := newRegistry()

	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg),
		runner.WithPrompt(errPromptSource{err: errors.New("disk on fire")}),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "ping",
	})
	if !errors.Is(res.Err, runner.ErrPromptRender) {
		t.Errorf("res.Err: errors.Is = false; got %v", res.Err)
	}
	if res.Reason != runner.TerminalError {
		t.Errorf("Reason = %v, want TerminalError", res.Reason)
	}
}

// errCompactor always returns a fixed error.
type errCompactor struct{ err error }

func (e errCompactor) Compact(_ context.Context, _ []llm.Message, _ int) (compact.Result, error) {
	return compact.Result{}, e.err
}

func TestErr_Compact_IsErrCompact(t *testing.T) {
	t.Parallel()
	// Two iterations: tool call (so the loop reaches iter>0 where
	// the compactor is consulted), then text-only.
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("a", "echo", `{}`), chunkDone()},
			{chunkText("ok"), chunkDone()},
		},
	}
	reg := newRegistry(stubTool{name: "echo"})

	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithCompactor(errCompactor{err: errors.New("squeeze failed")}),
	)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if !errors.Is(res.Err, runner.ErrCompact) {
		t.Errorf("res.Err: errors.Is(err, ErrCompact) = false; got %v", res.Err)
	}
	if res.Reason != runner.TerminalError {
		t.Errorf("Reason = %v, want TerminalError", res.Reason)
	}
}

func TestErr_InvalidIterations(t *testing.T) {
	t.Parallel()
	r := runner.New(nil)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:            taskscope.ID(uuid.NewString()),
		Prompt:        "x",
		MaxIterations: -1,
	})
	if !errors.Is(res.Err, runner.ErrInvalidIterations) {
		t.Errorf("errors.Is(res.Err, ErrInvalidIterations) = false; got %v", res.Err)
	}
	if res.Reason != runner.TerminalError {
		t.Errorf("Reason = %v, want TerminalError", res.Reason)
	}
}

func TestErr_Cancelled_IsErrCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Construct a runner with a ConversationLock that's already
		// active. Wait on a ctx we'll cancel; the runner should return
		// TerminalCancelled with res.Err wrapping ErrCancelled.
		lock := runner.NewConversationLock()
		lock.Acquire()

		prov := &fakeProvider{turns: nil}
		reg := newRegistry()
		r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg),
			runner.WithConversationLock(lock),
			runner.WithMaxIterations(3),
		)

		ctx, cancel := context.WithCancel(t.Context())

		type result struct {
			res runner.TaskResult
		}
		ch := make(chan result, 1)
		go func() {
			res := r.Run(ctx, runner.TaskSpec{
				ID:     taskscope.ID(uuid.NewString()),
				Prompt: "blocked",
			})
			ch <- result{res}
		}()

		// Run is now inside lock.Wait. synctest.Wait blocks until
		// every goroutine in the bubble is blocked, so we know the
		// cancel below races nothing.
		synctest.Wait()
		cancel()

		got := <-ch
		if got.res.Reason != runner.TerminalCancelled {
			t.Errorf("Reason = %v, want TerminalCancelled", got.res.Reason)
		}
		if !errors.Is(got.res.Err, runner.ErrCancelled) {
			t.Errorf("res.Err: errors.Is(err, ErrCancelled) = false; got %v", got.res.Err)
		}
		if !errors.Is(got.res.Err, context.Canceled) {
			t.Errorf("res.Err should also wrap context.Canceled; got %v", got.res.Err)
		}
	})
}
