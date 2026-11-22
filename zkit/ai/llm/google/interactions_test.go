package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const interactionModel = "gemini-3.8-flash"
const interactionAnswer = `{"id":"ignored-server-id","status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"answer"}]}]}`

func interactionFrame(data string) string { return "data: " + data + "\n\n" }

func interactionSSE(steps []string, status, usage string) string {
	var frames strings.Builder
	frames.WriteString(interactionFrame(`{"event_type":"interaction.created","interaction":{"id":"ignored","status":"in_progress"}}`))
	for index, step := range steps {
		fmt.Fprintf(&frames, "data: {\"event_type\":\"step.start\",\"index\":%d,\"step\":%s}\n\n", index, step)
		fmt.Fprintf(&frames, "data: {\"event_type\":\"step.stop\",\"index\":%d}\n\n", index)
	}
	if usage != "" {
		usage = `,"usage":` + usage
	}
	frames.WriteString(interactionFrame(`{"event_type":"interaction.completed","interaction":{"id":"ignored","status":"` + status + `"` + usage + `}}`))
	frames.WriteString("data: [DONE]\n\n")
	return frames.String()
}

func interactionProvider(t *testing.T, transport http.RoundTripper, opts ...func(*genai.ClientConfig)) *google.Provider {
	t.Helper()
	config := &genai.ClientConfig{APIKey: "fake-only", Backend: genai.BackendGeminiAPI, HTTPClient: &http.Client{Transport: transport}}
	for _, opt := range opts {
		opt(config)
	}
	client, err := genai.NewClient(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := google.NewProvider("", google.WithClient(client), google.WithInteractionsAPI(true), google.WithModel(interactionModel))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func interactionResponse(r *http.Request, contentType, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func collectInteractions(ctx context.Context, provider llm.Provider, req llm.CompletionRequest) ([]llm.CompletionChunk, error) {
	var chunks []llm.CompletionChunk
	for chunk, err := range provider.Complete(ctx, req) {
		if err != nil {
			if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
				return chunks, fmt.Errorf("nonzero error chunk: %w", err)
			}
			return chunks, err
		}
		chunks = append(chunks, chunk.Clone())
	}
	return chunks, nil
}

func interactionRequest(stream bool) llm.CompletionRequest {
	return llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}
}

func TestInteractionsWireAndLazySelection(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			t.Parallel()
			calls := 0
			transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v1beta/interactions" || r.Method != http.MethodPost {
					t.Errorf("route = %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				for key, want := range map[string]any{"store": false, "background": false, "stream": streaming, "model": interactionModel} {
					got, exists := body[key]
					if !exists || got != want {
						t.Errorf("%s = %#v (present=%t)", key, got, exists)
					}
				}
				if _, exists := body["previous_interaction_id"]; exists {
					t.Error("server history selected")
				}
				if streaming {
					return interactionResponse(r, "text/event-stream", interactionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`}, "completed", "")), nil
				}
				return interactionResponse(r, "application/json", interactionAnswer), nil
			}}
			provider := interactionProvider(t, transport)
			lazy := provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: "bad"}}})
			if calls != 0 {
				t.Fatal("eager work")
			}
			var err error
			for _, err = range lazy {
				break
			}
			if !errors.Is(err, llm.ErrModelNotSupported) || calls != 0 {
				t.Fatalf("lazy validation = %v, calls=%d", err, calls)
			}
			chunks, err := collectInteractions(t.Context(), provider, interactionRequest(streaming))
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || chunks[0].Content != "answer" || chunks[len(chunks)-1].FinishReason != llm.FinishReasons.STOP {
				t.Fatalf("calls=%d chunks=%+v", calls, chunks)
			}
		})
	}
}

func TestInteractionsRejectBeforeHTTP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		req       llm.CompletionRequest
		configure func(*genai.ClientConfig)
		want      error
	}{
		{name: "vertex", req: interactionRequest(false), configure: func(c *genai.ClientConfig) {
			c.Backend = genai.BackendVertexAI
			c.Project = "fake"
			c.Location = "global"
		}, want: llm.ErrModelNotSupported},
		{name: "version", req: interactionRequest(false), configure: func(c *genai.ClientConfig) { c.HTTPOptions.APIVersion = "v1" }, want: llm.ErrModelNotSupported},
		{name: "role", req: llm.CompletionRequest{Messages: []llm.Message{{Role: "secret-role"}}}, want: llm.ErrModelNotSupported},
		{name: "image", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage}}}}}, want: llm.ErrModelNotSupported},
		{name: "tool-media", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleTool, Content: "keep", Parts: []llm.ContentPart{{Type: llm.ContentTypeImage}}}}}, want: llm.ErrModelNotSupported},
		{name: "budget", req: llm.CompletionRequest{Thinking: llm.ThinkingConfig{BudgetTokens: 1}}, want: llm.ErrModelNotSupported},
		{name: "raw-zero-budget", req: llm.CompletionRequest{Options: llm.ModelOptions{"thinking_budget": 0}}, want: llm.ErrModelNotSupported},
		{name: "level", req: llm.CompletionRequest{Options: llm.ModelOptions{"thinking_level": "secret"}}, want: llm.ErrModelNotSupported},
		{name: "unknown-control", req: llm.CompletionRequest{Options: llm.ModelOptions{"secret": true}}, want: llm.ErrModelNotSupported},
		{name: "template", req: llm.CompletionRequest{ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: true}}, want: llm.ErrModelNotSupported},
		{name: "server-tool", req: llm.CompletionRequest{Tools: []llm.Tool{{Type: "google_search"}}}, want: llm.ErrModelNotSupported},
		{name: "orphan-result", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleTool, ToolCallID: "orphan"}}}, want: google.ErrInteractionsProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			transport := &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("secret-response") }}
			var opts []func(*genai.ClientConfig)
			if test.configure != nil {
				opts = append(opts, test.configure)
			}
			provider := interactionProvider(t, transport, opts...)
			_, err := collectInteractions(t.Context(), provider, test.req)
			if !errors.Is(err, test.want) || calls != 0 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			cause := errors.New("cancel cause")
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(cause)
			_, err = collectInteractions(ctx, provider, test.req)
			if !errors.Is(err, cause) || calls != 0 {
				t.Fatalf("cancel validation priority = %v", err)
			}
		})
	}
}

func TestInteractionsNativeReplay(t *testing.T) {
	t.Parallel()
	steps := []string{
		`{"type":"thought","signature":"signed-secret","summary":[{"type":"text","text":"reason"}]}`,
		`{"type":"model_output","content":[{"type":"text","text":"before","annotations":[]}]}`,
		`{"type":"function_call","id":"a","name":"echo","arguments":{"n":9007199254740993}}`,
		`{"type":"model_output","content":[{"type":"text","text":"after"}]}`,
		`{"type":"function_call","id":"b","name":"echo","arguments":{}}`,
	}
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			t.Parallel()
			var bodies []map[string]any
			transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				decoder := json.NewDecoder(r.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&body); err != nil {
					return nil, err
				}
				bodies = append(bodies, body)
				if streaming {
					return interactionResponse(r, "text/event-stream", interactionSSE(steps, "requires_action", "")), nil
				}
				return interactionResponse(r, "application/json", `{"status":"requires_action","steps":[`+strings.Join(steps, ",")+`]}`), nil
			}}
			provider := interactionProvider(t, transport)
			chunks, err := collectInteractions(t.Context(), provider, interactionRequest(streaming))
			if err != nil {
				t.Fatal(err)
			}
			assistant := llm.Message{Role: llm.RoleAssistant}
			for _, chunk := range chunks {
				assistant.Content += chunk.Content
				assistant.ReasoningContent += chunk.Thinking
				assistant.ToolCalls = append(assistant.ToolCalls, chunk.ToolCalls...)
				assistant.ContinuationItems = append(assistant.ContinuationItems, chunk.CompletedItems...)
			}
			if assistant.Content != "beforeafter" || assistant.ReasoningContent != "reason" || len(assistant.ToolCalls) != 2 || !strings.Contains(assistant.ToolCalls[0].Function.Arguments, "9007199254740993") {
				t.Fatalf("assistant=%+v", assistant)
			}
			slices.Reverse(assistant.ContinuationItems)
			req := interactionRequest(streaming)
			req.Messages = append(req.Messages, assistant, llm.Message{Role: llm.RoleTool, ToolCallID: "a", Content: "first"}, llm.Message{Role: llm.RoleTool, ToolCallID: "b", Content: "second"})
			before, _ := json.Marshal(req.Messages)
			if _, err := collectInteractions(t.Context(), provider, req); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(req.Messages)
			if string(before) != string(after) {
				t.Fatal("borrowed history mutated")
			}
			input := bodies[1]["input"].([]any)
			var want []any
			decodeNativeTestJSON(t, []byte("["+strings.Join(steps, ",")+"]"), &want)

			if !reflect.DeepEqual(input[1:6], want) {
				t.Fatalf("replay=%#v want=%#v", input[1:6], want)
			}
			if input[6].(map[string]any)["call_id"] != "a" || input[6].(map[string]any)["result"] != "first" {
				t.Fatalf("results=%+v", input[6:])
			}
			for _, mutate := range []struct {
				name   string
				change func(*llm.Message)
				want   error
			}{
				{"display", func(m *llm.Message) { m.Content = "secret-mismatch" }, google.ErrInteractionsProtocol},
				{"reasoning", func(m *llm.Message) { m.ReasoningContent = "secret-mismatch" }, google.ErrInteractionsProtocol},
				{"parts", func(m *llm.Message) { m.Parts = []llm.ContentPart{{Type: llm.ContentTypeText, Text: "mismatch"}} }, google.ErrInteractionsProtocol},
				{"id", func(m *llm.Message) { m.ToolCalls[0].ID = "wrong" }, google.ErrInteractionsProtocol},
				{"arguments", func(m *llm.Message) { m.ToolCalls[0].Function.Arguments = `{"n":0}` }, google.ErrInteractionsProtocol},
				{"protocol", func(m *llm.Message) { m.ContinuationItems[0].Format = "generate_content.part.v2" }, llm.ErrModelNotSupported},
				{"model", func(m *llm.Message) {
					m.ContinuationItems[0].Data = []byte(`{"model":"other","step":{"type":"thought"}}`)
				}, google.ErrInteractionsProtocol},
			} {
				t.Run(mutate.name, func(t *testing.T) {
					changed := req
					changed.Messages = slices.Clone(req.Messages)
					message := req.Messages[1].Clone()
					mutate.change(&message)
					changed.Messages[1] = message
					count := len(bodies)
					_, err := collectInteractions(t.Context(), provider, changed)
					if !errors.Is(err, mutate.want) || len(bodies) != count {
						t.Fatalf("err=%v requests=%d", err, len(bodies)-count)
					}
					if strings.Contains(err.Error(), "secret") {
						t.Fatalf("unsafe diagnostic: %v", err)
					}
				})
			}
			// Switching the same native history back to GenerateContent must fail.
			generated, err := google.NewProvider("fake-only", google.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			count := len(bodies)
			_, err = collectInteractions(t.Context(), generated, req)
			if !errors.Is(err, llm.ErrModelNotSupported) || len(bodies) != count {
				t.Fatalf("reciprocal guard=%v", err)
			}
		})
	}
}

func TestInteractionsAuthoritativePartsAndSchema(t *testing.T) {
	t.Parallel()
	var body map[string]any
	transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			return nil, err
		}
		return interactionResponse(r, "application/json", interactionAnswer), nil
	}}
	provider := interactionProvider(t, transport)
	schema := tools.SchemaFor[struct {
		Choice string `json:"choice"`
	}]()
	req := interactionRequest(false)
	req.Messages = []llm.Message{{Role: llm.RoleSystem, Content: "old", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "system"}}}, {Role: llm.RoleUser, Content: "discard", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: ""}, {Type: llm.ContentTypeText, Text: "  "}}}}
	req.Temperature = 0.5
	req.MaxTokens = 77
	req.Tools = []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "echo", Parameters: schema}}}
	req.ResponseFormat = llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: schema}
	before, _ := json.Marshal(req.Messages)
	if _, err := collectInteractions(t.Context(), provider, req); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(req.Messages)
	if string(before) != string(after) {
		t.Fatal("mutated request")
	}
	if body["system_instruction"] != "system" {
		t.Fatalf("system=%+v", body)
	}
	text := body["input"].([]any)[0].(map[string]any)["content"].([]any)
	if len(text) != 2 || text[0].(map[string]any)["text"] != "" || text[1].(map[string]any)["text"] != "  " {
		t.Fatalf("Parts=%+v", text)
	}
	format := body["response_format"].(map[string]any)
	if format["type"] != "text" || format["mime_type"] != "application/json" || !reflect.DeepEqual(format["schema"], body["tools"].([]any)[0].(map[string]any)["parameters"]) {
		t.Fatalf("schema=%+v", body)
	}
	req.ResponseFormat = llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}
	if _, err := collectInteractions(t.Context(), provider, req); err != nil {
		t.Fatal(err)
	}
	if body["response_format"].(map[string]any)["mime_type"] != "application/json" {
		t.Fatalf("object=%+v", body)
	}
}

func TestInteractionsConformance(t *testing.T) {
	providertest.Run(t, providertest.Suite{
		Factory: func(t *testing.T, url string) llm.Provider {
			t.Helper()
			p, err := google.NewProvider("fake-only", google.WithBaseURL(url), google.WithInteractionsAPI(true))
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
		Scenarios: []providertest.Scenario{{Name: "SuccessfulEOF", Request: interactionRequest(true), Handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, interactionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`}, "completed", `{"total_input_tokens":2,"total_output_tokens":3,"total_tokens":5}`))
		}, Assert: providertest.AssertUsageReported},
			{Name: "CancellationHeaders", Request: interactionRequest(true), Handler: func(_ http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }, Assert: providertest.AssertCancellationHonoured, CancelMidStream: true, Timeout: 3 * time.Second},
			{Name: "CancellationRead", Request: interactionRequest(true), Handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, interactionFrame(`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"answer"}]}}`)+interactionFrame(`{"event_type":"step.stop","index":0}`))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}, Assert: providertest.AssertCancellationHonoured, CancelMidStream: true},
		},
	})
}

func TestInteractionsExplicitFalseRetainsGenerateContent(t *testing.T) {
	t.Parallel()
	calls := 0
	transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("route=%s", r.URL.Path)
		}
		return interactionResponse(r, "application/json", `{"candidates":[{"content":{"parts":[{"text":"legacy"}]},"finishReason":"STOP"}]}`), nil
	}}
	provider, err := google.NewProvider("fake-only", google.WithHTTPClient(&http.Client{Transport: transport}), google.WithInteractionsAPI(true), google.WithInteractionsAPI(false))
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := collectInteractions(t.Context(), provider, interactionRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || chunks[0].Content != "legacy" {
		t.Fatalf("calls=%d chunks=%+v", calls, chunks)
	}
}

func TestInteractionsInvalidNativeAndToolHistory(t *testing.T) {
	t.Parallel()
	native := func(step string) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: "interactions.step.v1", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"model":"` + interactionModel + `","step":` + step + `}`)}}}
	}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Type: "function", Function: llm.ToolCallFunction{Name: "echo", Arguments: `{}`}}}}
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "a", Content: "ok"}
	tests := []struct {
		name     string
		messages []llm.Message
		want     error
	}{
		{"server-native", []llm.Message{native(`{"type":"google_search_call"}`)}, llm.ErrModelNotSupported},
		{"unknown-native", []llm.Message{native(`{"type":"secret-step"}`)}, llm.ErrModelNotSupported},
		{"media-native", []llm.Message{native(`{"type":"model_output","content":[{"type":"image","mime_type":"image/png","data":"secret-image"}]}`)}, llm.ErrModelNotSupported},
		{"malformed-envelope", []llm.Message{native(`secret-invalid`)}, google.ErrInteractionsProtocol},
		{"null-arguments", []llm.Message{native(`{"type":"function_call","id":"a","name":"echo","arguments":null}`), result}, google.ErrInteractionsProtocol},
		{"no-result", []llm.Message{call}, google.ErrInteractionsProtocol},
		{"duplicate-result", []llm.Message{call, result, result}, google.ErrInteractionsProtocol},
		{"duplicate-call", []llm.Message{call, call, result}, google.ErrInteractionsProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("secret-transport") }})
			req := interactionRequest(false)
			req.Messages = append(req.Messages, test.messages...)
			_, err := collectInteractions(t.Context(), provider, req)
			if !errors.Is(err, test.want) || calls != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}
