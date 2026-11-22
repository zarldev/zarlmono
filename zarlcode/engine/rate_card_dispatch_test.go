package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type dispatchCardSink struct {
	runner.NopSink
	events []runner.ProviderAttemptSettled
}

func (*dispatchCardSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*dispatchCardSink) PlanUpdated(string, code.Plan)    {}

func (s *dispatchCardSink) OnProviderAttemptSettled(_ context.Context, event runner.ProviderAttemptSettled) {
	if event.RateCard != nil {
		// Retain the observed basis before mutating the sink-owned snapshot.
		// Neither mutation may reach the runner's result or live target.
		card := event.RateCard.Clone()
		event.RateCard.Reference.Revision = "sink mutation"
		event.RateCard.CacheWrites[0].Rate.PerMillionTokens = 999
		event.RateCard = &card
	}
	s.events = append(s.events, event)
}

func TestLiveRunnerRateCardDispatch(t *testing.T) {
	t.Parallel()
	for _, headless := range []bool{false, true} {
		for _, bound := range []bool{false, true} {
			name := "recorded"
			if headless {
				name = "headless"
			}
			if bound {
				name += "/selected"
			} else {
				name += "/absent"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				testLiveRunnerRateCardDispatch(t, headless, bound)
			})
		}
	}
}

func testLiveRunnerRateCardDispatch(t *testing.T, headless, bound bool) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		for _, forbidden := range []string{"local-estimate-policy", "pricing_provider", "cache_writes", "fresh_input_per_million", "rate_card"} {
			if strings.Contains(string(body), forbidden) || strings.Contains(r.Header.Get("X-Rate-Card"), forbidden) {
				t.Errorf("local pricing field %q transmitted", forbidden)
			}
		}
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		if request.Model != "claude/gpt-Gemini Production Alias" || !request.Stream {
			t.Errorf("request = %+v", request)
		}
		if calls.Load() == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"id":"dispatch","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	settings := preparedGatewaySettings(server.URL)
	update, err := settings.PrepareTarget(t.Context(), engine.ProviderSpec{Name: "gateway-route", APIKey: "local-test-credential"})
	if err != nil {
		t.Fatal(err)
	}
	// Selection uses the actual built codec and the empty registered-default
	// URL representation, not the protocol override intent or resolved URL.
	if update.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || update.Spec.BaseURL != "" || update.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatal("preparation lost registered-default URL or actual codec")
	}
	target := engine.RateCardTarget{Provider: update.Spec.Name, Model: llm.ModelID(update.Spec.Model), BaseURL: update.Spec.BaseURL, Protocol: update.Facts.SelectedProtocol}
	if bound {
		update, err = engine.BindTargetRateCard(update, targetCardConfig(t, target, "v1"))
		if err != nil {
			t.Fatal(err)
		}
	}
	var want *llm.RateCard
	if bound {
		card := update.RateCard.Clone()
		want = &card
	}
	store, err := db.Open(t.Context(), t.TempDir()+"/dispatch.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	initializeControlSession(t, store, "dispatch-session")
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sink := &dispatchCardSink{}
	live := engine.NewLiveRunner(update.Provider, workspace, update.Spec.Model, engine.WithLiveSink(sink))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.ApplyTarget(update); err != nil {
		reservation.Release()
		t.Fatal(err)
	}
	reservation.Release()
	if calls.Load() != 0 {
		t.Fatal("preparation, binding, construction or publication invoked provider")
	}
	done := make(chan runner.TaskResult, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	go func() {
		if headless {
			done <- live.RunHeadless(ctx, "respond", 1)
			return
		}
		done <- runner.TaskResult{Err: live.RunRecordedTurn(ctx, "respond", nil, store, "dispatch-session")}
	}()
	// Always cancel and join the run, including an early test failure.
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	select {
	case <-started:
	case result := <-done:
		joined = true
		t.Fatalf("run ended before request: %v", result.Err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if bound {
		// A later policy and mutations of retained caller/snapshot values must
		// not reprice the already invoked attempt.
		next, bindErr := engine.BindTargetRateCard(update, targetCardConfig(t, target, "v2"))
		if bindErr != nil {
			t.Fatal(bindErr)
		}
		update.RateCard.Reference.Revision = "caller mutation"
		update.RateCard.CacheWrites[0].Rate.PerMillionTokens = 88
		capture := live.RunTarget()
		capture.RateCard.CacheWrites[0].Rate.PerMillionTokens = 77
		live.ApplyTarget(next)
	}
	close(release)
	result := <-done
	joined = true
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if calls.Load() != 1 || len(sink.events) != 1 {
		t.Fatalf("requests=%d settled=%d", calls.Load(), len(sink.events))
	}
	event := sink.events[0]
	if bound {
		if !reflect.DeepEqual(event.RateCard, want) {
			t.Fatalf("settled frozen basis = %+v; want %+v", event.RateCard, want)
		}
		active := live.RunTarget()
		if active.RateCard.Reference.Revision != "v2" || active.RateCard.CacheWrites[0].Rate.PerMillionTokens != 2.5 || active.Facts.Resolution.Descriptor.RateCard != active.RateCard.Reference {
			t.Fatal("sink/caller/snapshot changed active basis")
		}
	} else if event.RateCard != nil {
		t.Fatal("absent basis inferred a card")
	}
	if headless {
		if result.Reason != runner.TerminalCompleted || len(result.ProviderObservations) != 1 {
			t.Fatalf("headless result = %+v", result)
		}
		if !reflect.DeepEqual(result.ProviderObservations[0].RateCard, want) {
			t.Fatalf("frozen result basis = %+v; want %+v", result.ProviderObservations[0].RateCard, want)
		}
	} else if len(live.RecordedHistory("dispatch-session")) == 0 {
		t.Fatal("recorded turn did not retain canonical history")
	}
}
