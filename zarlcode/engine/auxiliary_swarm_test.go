package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// This transport keeps the actual OpenAI receiver/projection and task context,
// but all inference is fulfilled locally by the scripted provider.
type auxiliaryWireTransport struct{ provider *auxiliaryRunProvider }

func (tr auxiliaryWireTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var req llm.CompletionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	var wire struct {
		ResponseFormat struct {
			JSONSchema struct {
				Name string `json:"name"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	req.ResponseFormat.Name = wire.ResponseFormat.JSONSchema.Name
	var body bytes.Buffer
	for chunk, err := range tr.provider.Complete(r.Context(), req) {
		if err != nil {
			return nil, err
		}
		delta := map[string]any{"content": chunk.Content}
		if len(chunk.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(chunk.ToolCalls))
			for index, call := range chunk.ToolCalls {
				calls = append(calls, map[string]any{"index": index, "id": call.ID, "type": "function", "function": call.Function})
			}
			delta["tool_calls"] = calls
		}
		payload, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&body, "data: %s\n\n", payload)
		if chunk.UsageReported {
			fmt.Fprintf(&body, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens, chunk.Usage.TotalTokens)
		}
	}
	body.WriteString("data: [DONE]\n\n")
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String())), Request: r}, nil
}

func TestAuxiliarySwarmActualHelpersSettleExactlyOnce(t *testing.T) {
	p := &auxiliaryRunProvider{recurse: true, siblings: true}
	wire := openai.NewProvider("fixture-key", openai.WithHTTPClient(&http.Client{Transport: auxiliaryWireTransport{provider: p}}), openai.WithResponsesAPI(false))
	var mu sync.Mutex
	var observations []engine.AuxiliaryObservation
	live := newAuxiliaryRunner(t, wire, func(_ context.Context, o engine.AuxiliaryObservation) {
		mu.Lock()
		observations = append(observations, o.Clone())
		mu.Unlock()
	})
	goal := engine.NewSwarm("auxiliary scoped accounting")
	if _, err := goal.Add("member", live, "Exercise actual helper inference and descendants."); err != nil {
		t.Fatal(err)
	}
	startEngineSwarm(t, goal)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := goal.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	main, judges, planners := p.mainCalls, p.judges, p.planners
	p.mu.Unlock()
	got := goal.InferenceSnapshot()
	if judges != 5 || planners != 4 || got.Admitted != main+judges+planners || got.Settled != got.Admitted || got.Active != 0 || got.Unknown != 0 || got.Tokens != 3*main+1100*(judges+planners) {
		t.Fatalf("account=%+v main=%d judge=%d planner=%d", got, main, judges, planners)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observations) != judges+planners {
		t.Fatalf("callbacks=%d", len(observations))
	}
	depths := map[int]int{}
	for _, o := range observations {
		depths[o.Inference.Depth]++
		if o.Inference.TaskID == "" || !o.ResponseValid || o.Inference.Provider != "openai" || o.Inference.Model != "root-model" {
			t.Fatalf("observation=%+v", o)
		}
	}
	if depths[0] != 3 || depths[1] != 4 || depths[2] != 2 {
		t.Fatalf("tool-owner depths=%v", depths)
	}
}
