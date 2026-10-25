package main_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type parkedLiveProvider struct {
	llm.Provider
	calls   int
	started chan struct{}
	exited  bool
	failure error
}

func (p *parkedLiveProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	p.calls++
	if p.calls != 7 { // Research finished; now hold its implementation-notice follow-up.
		return p.Provider.Complete(ctx, request)
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.started)
		<-ctx.Done()
		yield(llm.CompletionChunk{}, errors.Join(ctx.Err(), p.failure))
		p.exited = true
	}
}

type waitForPeerProvider struct {
	llm.Provider
	calls   int
	started <-chan struct{}
}

func (p *waitForPeerProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	p.calls++
	return func(yield func(llm.CompletionChunk, error) bool) {
		if p.calls == 3 {
			select {
			case <-p.started:
			case <-ctx.Done():
				yield(llm.CompletionChunk{}, ctx.Err())
				return
			}
		}
		p.Provider.Complete(ctx, request)(yield)
	}
}

func TestVerifiedStopJoinsInflightPeerAndPreservesRealFailure(t *testing.T) {
	failure := errors.New("independent provider failure during shutdown")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "expected cancellation"},
		{name: "genuine failure joined with cancellation", err: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				agents := fixtureAgents()
				peer := &parkedLiveProvider{Provider: agents[0].Provider, started: make(chan struct{}), failure: tc.err}
				agents[0].Provider = peer
				agents[2].Provider = &waitForPeerProvider{Provider: agents[2].Provider, started: peer.started}
				report, err := experiment.RunLiveExperiment(t.Context(), agents)
				if !errors.Is(err, tc.err) || report.Complete != (tc.err == nil) {
					t.Fatalf("complete=%t err=%v, want err=%v", report.Complete, err, tc.err)
				}
				if !report.VerifiedSuccess || !report.ShutdownJoined || !peer.exited {
					t.Fatalf("verified stop did not cancel and join peer: %+v", report)
				}
				if tc.err == nil && !report.Members[0].CancelledForSuccess {
					t.Fatal("expected cancellation not classified")
				}
				if report.Members[0].Usage.UsageReports >= report.Members[0].Usage.Requests {
					t.Fatal("canceled request without usage was reported as fully metered")
				}
			})
		})
	}
}

func TestUnverifiedEvidenceDoesNotStopAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  int
		turn  int
		chunk llm.CompletionChunk
	}{
		{name: "failing oracle", role: 1, turn: 3, chunk: runnertest.ChunkToolCall("bad", "candidate_submit", `{"pattern":"^.*$"}`)},
		{name: "missing approval", role: 2, turn: 5, chunk: runnertest.ChunkToolCall("no-approval", "candidate_read", `{}`)},
		{name: "stale review", role: 2, turn: 5, chunk: runnertest.ChunkToolCall("stale", "candidate_review", `{"version":1,"approved":true}`)},
		{name: "wrong reply chain", role: 2, turn: 6, chunk: postChunk("wrong", "review", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				agents := fixtureAgents()
				provider := agents[tc.role].Provider.(*trialProvider)
				provider.turns[tc.turn] = tc.chunk
				report, err := experiment.RunLiveExperiment(t.Context(), agents)
				if !errors.Is(err, experiment.ErrIncomplete) || report.Complete || report.VerifiedSuccess || !report.ShutdownJoined {
					t.Fatalf("unverified evidence accepted: complete=%t verified=%t err=%v", report.Complete, report.VerifiedSuccess, err)
				}
			})
		})
	}
}

func TestVerifiedArtifactRejectsLaterToolMutation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agents := fixtureAgents()
		reviewer := agents[2].Provider.(*trialProvider)
		revoke := runnertest.ChunkToolCall("revoke", "candidate_review", `{"version":2,"approved":false}`)
		reviewer.turns[6].ToolCalls = append(reviewer.turns[6].ToolCalls, revoke.ToolCalls...)
		report, err := experiment.RunLiveExperiment(t.Context(), agents)
		if err != nil || !report.Complete || !report.Candidate.Approved || !report.VerifiedSuccess {
			t.Fatalf("accepted artifact changed after verification: complete=%t approved=%t err=%v", report.Complete, report.Candidate.Approved, err)
		}
	})
}

func TestLiveCallerCancellationIsNotVerifiedSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		report, err := experiment.RunLiveExperiment(ctx, fixtureAgents())
		if !errors.Is(err, context.Canceled) || report.VerifiedSuccess || report.Complete || !report.ShutdownJoined {
			t.Fatalf("caller cancellation hidden: complete=%t verified=%t err=%v", report.Complete, report.VerifiedSuccess, err)
		}
	})
}
