package main_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type rejectPostReviewProvider struct{ llm.Provider }

func (p rejectPostReviewProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	for _, message := range request.Messages {
		if message.ToolCallID == "review-post" {
			return failingProvider{err: errors.New("unnecessary post-review model request")}.Complete(ctx, request)
		}
	}
	return p.Provider.Complete(ctx, request)
}

func TestLiveStopsBeforePostReviewRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agents := fixtureAgents()
		agents[2].Provider = rejectPostReviewProvider{Provider: agents[2].Provider}
		report, err := experiment.RunLiveExperiment(t.Context(), agents)
		if err != nil || !report.Complete || !report.ShutdownJoined {
			t.Fatalf("verified result did not stop the goal: complete=%t joined=%t err=%v", report.Complete, report.ShutdownJoined, err)
		}
	})
}
