package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestProviderCapturesAndReplaysEncryptedReasoningItem(t *testing.T) {
	t.Parallel()
	const reasoning = `{"type":"reasoning","id":"rs_123","encrypted_content":"opaque-token","summary":[]}`
	requestNumber := 0
	var replay map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber++
		if requestNumber == 2 {
			if err := json.NewDecoder(r.Body).Decode(&replay); err != nil {
				t.Errorf("decode replay: %v", err)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requestNumber == 1 {
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_item\",\"call_id\":\"call_456\",\"name\":\"read\",\"arguments\":\"{}\"}}\n\n" +
				"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":" + reasoning + "}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"output_index\":2,\"delta\":\"answer\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	defer server.Close()

	provider, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true), openai.WithModel("gpt-5.6"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	request := llm.CompletionRequest{Stream: true, Thinking: llm.ThinkingConfig{Enabled: true}, Tools: []llm.Tool{responsesTool()}}
	var final llm.CompletionChunk
	for chunk, streamErr := range provider.Complete(t.Context(), request) {
		if streamErr != nil {
			t.Fatalf("first completion: %v", streamErr)
		}
		final = chunk.Clone()
	}
	if len(final.CompletedItems) != 1 {
		t.Fatalf("completed items = %+v", final.CompletedItems)
	}
	item := final.CompletedItems[0]
	if item.OutputIndex == nil || *item.OutputIndex != 1 || item.ID != "rs_123" || string(item.Data) != reasoning {
		t.Fatalf("continuation item = %+v data=%s", item, item.Data)
	}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].ID != "call_456" || item.ID == final.ToolCalls[0].ID {
		t.Fatalf("reasoning/tool IDs not distinct: item=%+v calls=%+v", item, final.ToolCalls)
	}

	request.Messages = []llm.Message{{
		Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(2), ToolCalls: final.ToolCalls,
		ContinuationItems: append(final.CompletedItems, llm.ContinuationItem{
			OutputIndex: llm.OutputPosition(4), Provider: "anthropic", Format: "content_block.v1", Kind: "thinking",
			Data: []byte(`{"type":"reasoning","id":"foreign","encrypted_content":"must-not-replay"}`),
		}),
	}}
	for _, streamErr := range provider.Complete(t.Context(), request) {
		if streamErr != nil {
			t.Fatalf("replay completion: %v", streamErr)
		}
	}
	input := replay["input"].([]any)
	if len(input) != 3 || input[0].(map[string]any)["type"] != "function_call" || input[0].(map[string]any)["call_id"] != "call_456" || input[1].(map[string]any)["type"] != "reasoning" || input[1].(map[string]any)["id"] != "rs_123" || input[1].(map[string]any)["encrypted_content"] != "opaque-token" || input[2].(map[string]any)["type"] != "message" {
		t.Fatalf("replayed input (including foreign provider item) = %#v", input)
	}
	if include := replay["include"].([]any); len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", include)
	}
}

func TestProviderCapturesDoneOnlyEncryptedReasoningItem(t *testing.T) {
	t.Parallel()
	const reasoning = `{"type":"reasoning","id":"rs_done","encrypted_content":"opaque-done","summary":[],"status":"completed"}`
	chunks, err := runResponses(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"output_index\":4,\"item\":" + reasoning + "}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}), nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	item := chunks[len(chunks)-1].CompletedItems[0]
	if item.OutputIndex == nil || *item.OutputIndex != 4 || item.ID != "rs_done" || string(item.Data) != reasoning {
		t.Fatalf("continuation item = %+v data=%s", item, item.Data)
	}
}

func TestProviderGatesResponsesControls(t *testing.T) {
	t.Parallel()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
	}))
	defer server.Close()
	provider, err := openai.NewProvider("test-key", openai.WithBaseURL(server.URL), openai.WithResponsesAPI(true), openai.WithModel("gpt-5.6"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	req := llm.CompletionRequest{Stream: true, Options: llm.ModelOptions{
		"reasoning_effort":       "max",
		"text_verbosity":         "low",
		"prompt_cache_key":       "session-1",
		"prompt_cache_retention": "24h",
		"pro_mode":               true,
		"previous_response_id":   "resp_ignored",
	}}
	for _, streamErr := range provider.Complete(t.Context(), req) {
		if streamErr != nil {
			t.Fatalf("Complete: %v", streamErr)
		}
	}
	if body["reasoning"].(map[string]any)["effort"] != "max" || body["text"].(map[string]any)["verbosity"] != "low" || body["prompt_cache_key"] != "session-1" || body["prompt_cache_retention"] != "24h" {
		t.Fatalf("supported controls = %#v", body)
	}
	for _, key := range []string{"pro_mode", "previous_response_id"} {
		if _, ok := body[key]; ok {
			t.Fatalf("unsupported %s present: %#v", key, body)
		}
	}
}

func TestProviderResponsesAPISelectionIsOptionOrderIndependent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		options []options.Option[openai.Provider]
	}{
		{name: "base URL then explicit responses", options: []options.Option[openai.Provider]{openai.WithResponsesAPI(true)}},
		{name: "explicit responses then base URL", options: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var path string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
			}))
			defer server.Close()
			opts := []options.Option[openai.Provider]{openai.WithBaseURL(server.URL)}
			if tc.options == nil {
				opts = []options.Option[openai.Provider]{openai.WithResponsesAPI(true), openai.WithBaseURL(server.URL)}
			} else {
				opts = append(opts, tc.options...)
			}
			opts = append(opts, openai.WithModel("gpt-5.6"))
			provider, err := openai.NewProvider("test-key", opts...)
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			for _, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true}) {
				if streamErr != nil {
					t.Fatalf("Complete: %v", streamErr)
				}
			}
			if path != "/responses" {
				t.Fatalf("request path = %q, want /responses", path)
			}
		})
	}
}
