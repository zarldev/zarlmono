package main_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// One serialized member owns this wrapper. The first reported usage snapshot
// places the trial at a deterministic threshold independent of worker timing.
type firstUsageProvider struct {
	llm.Provider
	tokens   int
	reported bool
}

func (p *firstUsageProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.Provider.Complete(ctx, request)(func(chunk llm.CompletionChunk, err error) bool {
			if chunk.UsageReported && !p.reported {
				p.reported = true
				chunk.Usage = llm.Usage{PromptTokens: p.tokens, TotalTokens: p.tokens}
			}
			return yield(chunk, err)
		})
	}
}

func TestLiveReportedTokenThreshold100K(t *testing.T) {
	for _, tokens := range []int{65000, 100000} {
		name := "continues above old 60K threshold"
		if tokens == 100000 {
			name = "rejects admission at 100K"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				agents := fixtureAgents()
				agents[0].Provider = &firstUsageProvider{Provider: agents[0].Provider, tokens: tokens}
				report, err := experiment.RunLiveExperiment(t.Context(), agents)
				if !report.ShutdownJoined {
					t.Fatal("trial did not join its workers")
				}
				if tokens == 100000 {
					if report.Complete || (!errors.Is(err, experiment.ErrLiveBudget) && !errors.Is(err, swarm.ErrBudget)) {
						t.Fatalf("threshold not enforced: complete=%t err=%v", report.Complete, err)
					}
					return
				}
				if err != nil || !report.Complete {
					t.Fatalf("trial below 100K stopped: complete=%t err=%v", report.Complete, err)
				}
				total := 0
				for _, member := range report.Members {
					total += member.Usage.TotalTokens
				}
				if total <= 60000 || total >= 100000 {
					t.Fatalf("unexpected reported usage: %d", total)
				}
				if !strings.Contains(report.Limits, "100000 reported-token admission threshold") {
					t.Fatalf("stale report policy: %s", report.Limits)
				}
			})
		})
	}
}
