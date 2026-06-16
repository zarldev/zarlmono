package runner_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// TestNew_DefaultsToEmptyToolSource covers the 4.1 fix: New without
// WithTools is a valid tool-less runner (empty registry default) rather
// than a deferred nil-panic in the loop.
func TestNew_DefaultsToEmptyToolSource(t *testing.T) {
	t.Parallel()
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{chunkText("done"), chunkDone()},
	}}
	r := runner.New(runner.ClientFromProvider(provider)) // no WithTools

	res := r.Run(context.Background(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "hi",
	})
	if res.Err != nil {
		t.Fatalf("Run on a tool-less runner: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("reason = %v, want completed", res.Reason)
	}
}
