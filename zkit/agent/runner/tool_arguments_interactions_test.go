package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type numericInteractionTransport func(*http.Request) (*http.Response, error)

func (f numericInteractionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStructuredNumericSignedInteractionsRunnerLoop(t *testing.T) {
	t.Parallel()
	steps := []string{
		`{"type":"thought","signature":"opaque-signed","summary":[{"type":"text","text":"reason"}]}`,
		`{"type":"model_output","content":[{"type":"text","text":"before","annotations":[{"type":"url_citation","start_index":0,"end_index":6,"url":"https://example.invalid/source"}]}]}`,
		`{"type":"function_call","id":"numeric","name":"exact","arguments":{"n":9007199254740993}}`,
	}
	var requests []map[string]any
	transport := numericInteractionTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1beta/interactions" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return nil, err
		}
		requests = append(requests, body)
		response := numericInteractionSSE(steps, "requires_action")
		if len(requests) > 1 {
			response = numericInteractionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"done"}]}`}, "completed")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
	})
	// Explicit fake API key and fully intercepted transport: no ADC, environment
	// credentials, real network requests, or server-side conversation state.
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{APIKey: "fake-test-key", Backend: genai.BackendGeminiAPI, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := google.NewProvider("", google.WithClient(client), google.WithInteractionsAPI(true), google.WithModel("gemini-3.8-flash"))
	if err != nil {
		t.Fatal(err)
	}
	type args struct {
		N int64 `json:"n"`
	}
	var executed []int64
	tool := tools.New(tools.ToolSpec{Name: "exact", Description: "exact integer", Parameters: tools.SchemaFor[args]()}, func(_ context.Context, a args) (string, error) {
		executed = append(executed, a.N)
		return fmt.Sprintf("executed:%d", a.N), nil
	})
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(tools.NewRegistry(tool)), runner.WithMaxIterations(3))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
	// Assert execution independently: the old runner rounded before GI1 rejected
	// the next request's contradictory signed native projection.
	if len(executed) != 1 || executed[0] != 9007199254740993 {
		t.Errorf("executed=%v want [9007199254740993]", executed)
	}
	if result.Err != nil || len(requests) != 2 || result.FinalContent != "done" || result.Iterations != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(requests))
	}
	for _, request := range requests {
		if _, exists := request["previous_interaction_id"]; exists {
			t.Fatal("server-side previous interaction selected")
		}
		if request["store"] != false || request["background"] != false || request["stream"] != true {
			t.Fatalf("request flags=%+v", request)
		}
	}
	input := requests[1]["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("replayed input=%+v", input)
	}
	for i, step := range steps {
		var want map[string]any
		decoder := json.NewDecoder(strings.NewReader(step))
		decoder.UseNumber()
		if err := decoder.Decode(&want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(input[i+1], want) {
			t.Errorf("signed/annotated native step %d changed: got=%+v want=%+v", i, input[i+1], want)
		}
	}
	if input[4].(map[string]any)["result"] != "executed:9007199254740993" {
		t.Fatalf("tool replay=%+v", input[4])
	}
	if result.Messages[1].ToolCalls[0].Function.Arguments != `{"n":9007199254740993}` {
		t.Fatalf("history=%+v", result.Messages[1])
	}
}

func numericInteractionSSE(steps []string, status string) string {
	var out strings.Builder
	out.WriteString("data: {\"event_type\":\"interaction.created\",\"interaction\":{\"id\":\"ignored\",\"status\":\"in_progress\"}}\n\n")
	for index, step := range steps {
		fmt.Fprintf(&out, "data: {\"event_type\":\"step.start\",\"index\":%d,\"step\":%s}\n\n", index, step)
		fmt.Fprintf(&out, "data: {\"event_type\":\"step.stop\",\"index\":%d}\n\n", index)
	}
	fmt.Fprintf(&out, "data: {\"event_type\":\"interaction.completed\",\"interaction\":{\"id\":\"ignored\",\"status\":%q}}\n\ndata: [DONE]\n\n", status)
	return out.String()
}
