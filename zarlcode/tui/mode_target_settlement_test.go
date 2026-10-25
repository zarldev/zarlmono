package tui_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

// Custom endpoints deliberately use ordinary durable saves rather than exact
// checkpoints. This exercises routing through real provider HTTP without any
// internal message manipulation or external network access.
func TestActiveModeTargetWaitsForDurableSettlementAndRoutesQueuedTurn(t *testing.T) {
	f := newBeforeFixture(t)
	step(t, f.ui, window(120, 40))
	f.ui.SetStartupReady(true)
	var planCalls atomic.Int32
	observedModel := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		planCalls.Add(1)
		observedModel <- request.Model
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"plan-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"planned\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"model\":\"plan-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	setPlanRoute(t, settings, "openai", "plan-model")
	f.ui.SetSettings(settings)
	base := f.live.RunTarget()
	base.Spec.BaseURL = server.URL + "/v1"
	base.Spec.APIKey = "sk-test-abcdefghijklmnopqrstuvwxyz0123456789"
	f.live.ApplyTarget(engine.TargetUpdate{Provider: f.provider, Spec: base.Spec, Window: base.Window})
	active := f.live.RunTarget().Spec
	var activeCalls atomic.Int32
	f.provider.check = func(context.Context) { activeCalls.Add(1) }
	driveBeforeCommand(f, f.ui.Submit("active build turn"))
	f.sink.Drain()
	f.mu.Lock()
	events := append([]tea.Msg(nil), f.events...)
	f.events = nil
	f.mu.Unlock()
	if len(events) < 2 {
		t.Fatal("missing terminal lifecycle")
	}
	started := -1
	for i, event := range events {
		f.ui.Update(event)
		if _, ok := event.(teasink.ConversationStartedMsg); ok {
			started = i
			break
		}
	}
	if started < 0 {
		t.Fatal("missing start")
	}
	f.ui.SetPlanMode(true)
	_, premature := f.ui.Update(nil)
	if premature != nil {
		t.Fatal("target built during active turn")
	}
	assertModeSettlementTarget(t, f, active, activeCalls.Load(), planCalls.Load())
	// Input accepted after the target change must survive until the new
	// target can be applied, even if this turn has no further steer drain.
	if cmd := f.ui.Submit("queued under plan"); cmd != nil || f.live.QueueLen() != 1 {
		t.Fatalf("input not queued after mode change: %s", f.ui.ToastText())
	}
	for _, event := range events[started+1 : len(events)-1] {
		f.ui.Update(event)
	}
	_, save := f.ui.Update(events[len(events)-1])
	if save == nil {
		t.Fatal("missing durable settlement")
	}
	assertModeSettlementTarget(t, f, active, activeCalls.Load(), planCalls.Load())
	persisted := save()
	assertModeSettlementTarget(t, f, active, activeCalls.Load(), planCalls.Load())
	_, prepare := f.ui.Update(persisted)
	if prepare == nil {
		t.Fatal("no mode preparation after settlement")
	}
	_, launch := f.ui.Update(prepare())
	if launch == nil {
		_, launch = f.ui.Update(nil)
	}
	if launch == nil {
		t.Fatal("prepared target did not promote queue")
	}
	driveBeforeCommand(f, launch)
	want := active
	want.Model = "plan-model"
	if got := f.live.RunTarget(); got.Spec != want || got.Model != want.Model {
		t.Fatalf("wrong physical target: %+v", got)
	}
	if activeCalls.Load() != 1 || planCalls.Load() != 1 || f.live.QueueLen() != 0 {
		t.Fatalf("dispatches active=%d plan=%d queue=%d", activeCalls.Load(), planCalls.Load(), f.live.QueueLen())
	}
	select {
	case model := <-observedModel:
		if model != want.Model {
			t.Fatalf("request model=%s", model)
		}
	default:
		t.Fatal("provider request not observed")
	}
}

func assertModeSettlementTarget(t *testing.T, f *beforeFixture, want engine.ProviderSpec, activeCalls, planCalls int32) {
	t.Helper()
	if got := f.live.RunTarget(); got.Spec != want || got.Model != want.Model {
		t.Fatalf("physical target changed before settlement: %+v", got)
	}
	if activeCalls != 1 || planCalls != 0 {
		t.Fatalf("premature dispatch active=%d plan=%d", activeCalls, planCalls)
	}
}
