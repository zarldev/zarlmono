package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestInteractionsRealRunnerToolLoop(t *testing.T) {
	t.Parallel()
	var requests []map[string]any
	provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			return interactionResponse(r, "text/event-stream", fragmentedInteractions()), nil
		}
		return interactionResponse(r, "text/event-stream", interactionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"done"}]}`}, "completed", "")), nil
	}})
	type args struct {
		Text string `json:"text"`
	}
	var executed atomic.Int32
	tool := tools.New(tools.ToolSpec{Name: "echo", Description: "Echo", Parameters: tools.SchemaFor[args]()}, func(_ context.Context, a args) (string, error) {
		executed.Add(1)
		return "result:" + a.Text, nil
	})
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(tools.NewRegistry(tool)), runner.WithMaxIterations(3))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != runner.TerminalCompleted || result.Iterations != 2 || result.FinalContent != "done" || len(requests) != 2 || executed.Load() != 2 {
		t.Fatalf("result=%+v requests=%d executed=%d", result, len(requests), executed.Load())
	}
	for _, request := range requests {
		if request["store"] != false || request["background"] != false || request["stream"] != true || request["previous_interaction_id"] != nil || len(request["tools"].([]any)) != 1 {
			t.Fatalf("request=%+v", request)
		}
	}
	input := requests[1]["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("full history=%+v", input)
	}
	for index, id := range []string{"first", "second"} {
		call := input[index+1].(map[string]any)
		response := input[index+3].(map[string]any)
		if call["id"] != id || response["call_id"] != id || !strings.Contains(response["result"].(string), "result:"+id) {
			t.Fatalf("call=%+v result=%+v", call, response)
		}
	}
}

func TestInteractionsSignedRealRunnerSmallNumber(t *testing.T) {
	t.Parallel()
	steps := []string{
		`{"type":"thought","signature":"opaque-signed","summary":[{"type":"text","text":"reason"}]}`,
		`{"type":"model_output","content":[{"type":"text","text":"before","annotations":[{"type":"url_citation","start_index":0,"end_index":6,"url":"https://example.invalid/source"}]}]}`,
		`{"type":"function_call","id":"a","name":"echo","arguments":{"text":"hello","n":42}}`,
	}
	var requests []map[string]any
	p := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return nil, err
		}
		requests = append(requests, body)
		if len(requests) == 1 {
			return interactionResponse(r, "text/event-stream", interactionSSE(steps, "requires_action", "")), nil
		}
		return interactionResponse(r, "text/event-stream", interactionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"done"}]}`}, "completed", "")), nil
	}})
	type args struct {
		Text string      `json:"text"`
		N    json.Number `json:"n"`
	}
	tool := tools.New(tools.ToolSpec{Name: "echo", Description: "echo", Parameters: tools.SchemaFor[args]()}, func(_ context.Context, a args) (string, error) { return a.Text + ":" + a.N.String(), nil })
	r := runner.New(runner.ClientFromProvider(p), runner.WithTools(tools.NewRegistry(tool)), runner.WithMaxIterations(3))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
	if result.Err != nil || len(requests) != 2 || result.FinalContent != "done" {
		t.Fatalf("result=%+v requests=%d", result, len(requests))
	}
	input := requests[1]["input"].([]any)
	if len(input) != 5 || requests[1]["previous_interaction_id"] != nil {
		t.Fatalf("replay=%+v", requests[1])
	}
	for i, s := range steps {
		var want map[string]any
		decodeNativeTestJSON(t, []byte(s), &want)
		if !reflect.DeepEqual(input[i+1], want) {
			t.Fatalf("step%d got=%+v want=%+v", i, input[i+1], want)
		}
	}
	if input[4].(map[string]any)["result"] != "hello:42" {
		t.Fatalf("tool result=%+v", input[4])
	}
}
