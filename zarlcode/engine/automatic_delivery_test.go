package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestAutomaticDeliveryEnabledByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		encoded, _ := json.Marshal(body)
		if !strings.Contains(string(encoded), "Completed child results arrive automatically") {
			t.Error("default runner omitted automatic-delivery instructions")
		}
		completionFixture(w, "The requested information is available.", "")
	}))
	defer server.Close()
	live := automaticFixtureRunner(t, server.URL)
	if err := live.RunTurn(t.Context(), "Describe the available information; no changes requested."); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticDeliveryRootQueueWakesWithoutWakingChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	release := make(chan struct{})
	var calls atomic.Int32
	var queuedSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		messages, _ := body["messages"].([]any)
		child := false
		queued := false
		for _, item := range messages {
			message := item.(map[string]any)
			if message["role"] != "user" {
				continue
			}
			content, _ := json.Marshal(message["content"])
			child = child || strings.Contains(string(content), "child fixture work")
			queued = queued || strings.Contains(string(content), "new root direction")
		}
		if child {
			if queued {
				t.Error("root queue leaked into child history")
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			completionFixture(w, "child evidence", "")
			return
		}
		switch calls.Add(1) {
		case 1:
			completionFixture(w, "", `{"prompt":"child fixture work"}`)
		case 2:
			completionFixture(w, "A provisional summary.", "")
		case 3:
			queuedSeen.Store(queued)
			completionFixture(w, "An updated provisional summary.", "")
		default:
			completionFixture(w, "The evidence is incorporated.", "")
		}
	}))
	defer server.Close()
	sink := &automaticFixtureSink{waiting: make(chan struct{}, 8)}
	live := automaticFixtureRunner(t, server.URL, engine.WithLiveSink(sink))
	live.SetLimits(1024, 6, 2, 2)
	var wg sync.WaitGroup
	var runErr error
	wg.Go(func() { runErr = live.RunTurn(ctx, "Summarize this information with a child; do not edit files.") })
	defer func() { cancel(); wg.Wait() }()
	wait := func() {
		t.Helper()
		select {
		case <-sink.waiting:
		case <-ctx.Done():
			t.Fatalf("parent did not wait: %v", context.Cause(ctx))
		}
	}
	wait()
	if got := calls.Load(); got != 2 {
		t.Fatalf("parent requests before queue = %d", got)
	}
	live.QueueInput("new root direction")
	wait()
	if !queuedSeen.Load() || calls.Load() != 3 {
		t.Fatalf("queue did not wake root exactly once: calls=%d, queued=%v", calls.Load(), queuedSeen.Load())
	}
	close(release)
	wg.Wait()
	if runErr != nil {
		t.Fatal(runErr)
	}
	observations := 0
	for _, message := range live.ContextSnapshot() {
		if message.Observation.Version != 0 {
			observations++
		}
	}
	if observations != 1 {
		t.Fatalf("automatic admissions in parent history = %d", observations)
	}
}

func automaticFixtureRunner(t *testing.T, url string, opts ...options.Option[engine.LiveRunner]) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	live := engine.NewLiveRunner(openai.NewProvider("fixture-key", openai.WithBaseURL(url)), ws, "fixture-model", opts...)
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return live
}

func completionFixture(w http.ResponseWriter, text, arguments string) {
	w.Header().Set("Content-Type", "text/event-stream")
	delta := map[string]any{"content": text}
	finish := "stop"
	if arguments != "" {
		delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "spawn-fixture", "type": "function", "function": map[string]any{"name": "agent_spawn", "arguments": arguments}}}}
		finish = "tool_calls"
	}
	data, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
}

type automaticFixtureSink struct {
	runner.NopSink
	waiting chan struct{}
}

func (s *automaticFixtureSink) OnWaitingForInputs(ctx context.Context, event runner.WaitingForInputs) {
	if event.Waiting && event.Depth == 0 {
		select {
		case s.waiting <- struct{}{}:
		case <-ctx.Done():
		}
	}
}
func (*automaticFixtureSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*automaticFixtureSink) PlanUpdated(string, code.Plan)    {}
