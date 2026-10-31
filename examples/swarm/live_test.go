package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type trialProvider struct {
	name       string
	turns      []llm.CompletionChunk
	calls      int
	dependency <-chan struct{}
	completed  chan struct{}
}

func (p *trialProvider) Name() string { return p.name }

func (p *trialProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.calls++ // One serialized member owns this provider throughout the trial.
		if request.MaxTokens != 2048 {
			yield(llm.CompletionChunk{}, errors.New("missing per-request token limit"))
			return
		}
		if p.calls == 3 && p.dependency != nil {
			select {
			case <-p.dependency:
			case <-ctx.Done():
				yield(llm.CompletionChunk{}, ctx.Err())
				return
			}
		}
		chunk := runnertest.ChunkText("Already contributed; idle without acknowledgments.")
		if p.calls <= len(p.turns) {
			chunk = p.turns[p.calls-1]
		}
		chunk.UsageReported = true
		chunk.Usage = llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}
		yield(chunk, nil)
		if p.calls == len(p.turns) && p.completed != nil {
			close(p.completed)
		}
	}
}

func postChunk(id, topic string, reply int) llm.CompletionChunk {
	return runnertest.ChunkToolCall(id, "board_post", fmt.Sprintf(`{"summary":%q,"body":"Fixture evidence from tools.","topic":%q,"reply_to":%d}`, topic, topic, reply))
}

func fixtureAgents() []experiment.LiveAgent {
	researched, implemented := make(chan struct{}), make(chan struct{})
	initial := func() []llm.CompletionChunk {
		return []llm.CompletionChunk{runnertest.ChunkToolCall("initial-list", "board_list", `{}`), runnertest.ChunkText("No task yet.")}
	}
	research := append(initial(),
		runnertest.ChunkToolCall("task", "board_read", `{"ids":[1]}`),
		runnertest.ChunkToolCall("baseline", "candidate_test", `{}`),
		postChunk("research", "research", 1), runnertest.ChunkText("Research published."))
	args, _ := json.Marshal(map[string]string{"pattern": validIPv4})
	implementation := append(initial(),
		runnertest.ChunkToolCall("read-research", "board_read", `{"ids":[2]}`),
		runnertest.ChunkToolCall("submit", "candidate_submit", string(args)),
		runnertest.ChunkToolCall("test", "candidate_test", `{}`),
		postChunk("implementation", "implementation", 2), runnertest.ChunkText("Implementation published."))
	review := append(initial(),
		runnertest.ChunkToolCall("read-implementation", "board_read", `{"ids":[3]}`),
		runnertest.ChunkToolCall("inspect", "candidate_read", `{}`),
		runnertest.ChunkToolCall("verify", "candidate_test", `{}`),
		runnertest.ChunkToolCall("review", "candidate_review", `{"version":2,"approved":true}`),
		postChunk("review-post", "review", 3), runnertest.ChunkText("Review published."))
	return []experiment.LiveAgent{
		{Role: "researcher", Model: "fixture-deepseek", Provider: &trialProvider{name: "deepseek", turns: research, completed: researched}},
		{Role: "implementer", Model: "fixture-sol", Provider: &trialProvider{name: "openai-codex", turns: implementation, dependency: researched, completed: implemented}},
		{Role: "reviewer", Model: "fixture-luna", Provider: &trialProvider{name: "openai-codex", turns: review, dependency: implemented}},
	}
}

func TestLiveHarnessRoutesUsageAndIndependentOracleOffline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agents := fixtureAgents()
		report, err := experiment.RunLiveExperiment(t.Context(), agents)
		if err != nil {
			t.Fatal(err)
		}
		if !report.Complete || !report.InitiallyIdle || !report.ShutdownJoined || report.Oracle.Passed != report.Oracle.Total || len(report.Findings) != 4 {
			t.Fatalf("incomplete trial: %+v", report)
		}
		requests := 0
		for index, member := range report.Members {
			if member.Model != agents[index].Model || member.Provider != agents[index].Provider.Name() {
				t.Errorf("route flattened: %+v", member)
			}
			if member.Usage.Requests == 0 || member.Usage.UsageReports != member.Usage.Requests || member.Usage.TotalTokens != member.Usage.Requests*110 {
				t.Errorf("incorrect usage: %+v", member.Usage)
			}
			requests += member.Usage.Requests
		}
		if requests > 36 {
			t.Fatalf("request ceiling exceeded: %d", requests)
		}
	})
}

type failingProvider struct{ err error }

func (failingProvider) Name() string { return "fixture" }

func (p failingProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) { yield(llm.CompletionChunk{}, p.err) }
}

func TestLiveHarnessProviderFailureJoinsWithoutFabricatingUsage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("fixture route unavailable")
		var agents []experiment.LiveAgent
		for _, role := range []string{"researcher", "implementer", "reviewer"} {
			agents = append(agents, experiment.LiveAgent{Role: role, Model: "fixture", Provider: failingProvider{err: failure}})
		}
		report, err := experiment.RunLiveExperiment(t.Context(), agents)
		if !errors.Is(err, failure) || report.Complete || !report.ShutdownJoined {
			t.Fatalf("report=%+v err=%v", report, err)
		}
		for _, member := range report.Members {
			if member.Usage.Requests == 0 || member.Usage.UsageReports != 0 || member.LastError == "" || !member.Stopped {
				t.Errorf("failure or missing usage hidden: %+v", member)
			}
		}
	})
}
