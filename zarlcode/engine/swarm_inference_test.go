package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestSwarmAccountIncludesActualChildInvocations(t *testing.T) {
	var parentCalls, childCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		child := false
		for _, m := range req.Messages {
			if m.Role == "user" && strings.Contains(string(m.Content), "accounted child assignment") {
				child = true
			}
		}
		delta := map[string]any{"content": "Evidence reviewed; no file changes requested."}
		finish := "stop"
		if child {
			childCalls.Add(1)
		} else if parentCalls.Add(1) == 1 {
			finish = "tool_calls"
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "spawn-accounted", "type": "function", "function": map[string]any{"name": "agent_spawn", "arguments": `{"prompt":"accounted child assignment","mode":"explore"}`}}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, err := json.Marshal(map[string]any{"id": "fixture", "model": "fixture-model", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, "data: %s\n\ndata: {\"id\":\"fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n", payload)
	}))
	defer server.Close()
	live := automaticFixtureRunner(t, server.URL, engine.WithReadOnlyTasks())
	live.SetLimits(1024, 8, 2, 2)
	goal := engine.NewSwarm("account child inference")
	if _, err := goal.Add("parent", live, "Delegate read-only inspection and summarize the evidence; no changes."); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	got := goal.InferenceSnapshot()
	want := int(parentCalls.Load() + childCalls.Load())
	if childCalls.Load() != 1 || parentCalls.Load() < 2 || got.Admitted != want || got.Settled != want || got.Active != 0 || got.Unknown != 0 || got.Tokens != 3*want {
		t.Fatalf("account=%+v parent=%d child=%d", got, parentCalls.Load(), childCalls.Load())
	}
}
