package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type inferenceIdentitySink struct {
	runner.NopSink
	mu              sync.Mutex
	provider, model string
	starts          int
}

func (s *inferenceIdentitySink) OnConversationStarted(_ context.Context, e runner.ConversationStarted) {
	if e.Depth != 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider, s.model = e.Provider, e.Model
	s.starts++
}

func (*inferenceIdentitySink) DiffEvent(diffrecorder.DiffEvent) {}
func (*inferenceIdentitySink) PlanUpdated(string, code.Plan)    {}

func TestSwarmRootIdentityDoesNotChangeOrdinaryAttribution(t *testing.T) {
	for _, accounted := range []bool{false, true} {
		name := "ordinary"
		if accounted {
			name = "accounted swarm"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				completionFixture(w, "done", "")
			}))
			t.Cleanup(server.Close)
			sink := &inferenceIdentitySink{}
			live := automaticFixtureRunner(t, server.URL, engine.WithLiveSink(sink))
			if accounted {
				goal := engine.NewSwarm("record known root identity")
				if _, err := goal.Add("root", live, "summarize without tools"); err != nil {
					t.Fatal(err)
				}
				startEngineSwarm(t, goal)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if err := goal.WaitIdle(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := live.RunTurn(t.Context(), "summarize without tools"); err != nil {
				t.Fatal(err)
			}
			sink.mu.Lock()
			provider, model, starts := sink.provider, sink.model, sink.starts
			sink.mu.Unlock()
			wantProvider, wantModel := "", ""
			if accounted {
				wantProvider, wantModel = "openai", "fixture-model"
			}
			if starts != 1 || provider != wantProvider || model != wantModel {
				t.Fatalf("root identity = %q/%q (%d starts), want %q/%q", provider, model, starts, wantProvider, wantModel)
			}
		})
	}
}
