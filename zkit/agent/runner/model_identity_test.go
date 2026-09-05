package runner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type modelIdentitySink struct {
	runner.NopSink
	started runner.ConversationStarted
}

func (s *modelIdentitySink) OnConversationStarted(event runner.ConversationStarted) {
	s.started = event
}

func TestWithModelIdentityLabelsConversationStart(t *testing.T) {
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText("done")}})
	sink := &modelIdentitySink{}
	r := runner.New(client,
		runner.WithSink(sink),
		runner.WithModelIdentity("anthropic", "claude-sonnet"),
	)

	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "review"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}
	if sink.started.Provider != "anthropic" || sink.started.Model != "claude-sonnet" {
		t.Fatalf("conversation target = %q/%q, want anthropic/claude-sonnet", sink.started.Provider, sink.started.Model)
	}
}
