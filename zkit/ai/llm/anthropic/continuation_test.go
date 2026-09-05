package anthropic_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestProviderCapturesAndExactlyReplaysNativeThinkingBlocks(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"echo","input":{"x":1}},{"type":"thinking","thinking":"think\nexactly ☃","signature":"sig+/= exact"},{"type":"text","text":"answer"},{"type":"redacted_thinking","data":"opaque+/= bytes"}],"model":"claude-test","stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	t.Cleanup(server.Close)

	provider, err := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	var first llm.CompletionChunk
	for chunk, completeErr := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}}) {
		if completeErr != nil {
			t.Fatalf("Complete: %v", completeErr)
		}
		if len(chunk.CompletedItems) != 0 {
			first = chunk.Clone()
		}
	}
	<-requests
	if first.Thinking != "think\nexactly ☃" || len(first.CompletedItems) != 3 {
		t.Fatalf("captured chunk = %#v", first)
	}
	if first.CompletedItems[0].OutputIndex == nil || *first.CompletedItems[0].OutputIndex != 1 || first.CompletedItems[0].Kind != "thinking" ||
		string(first.CompletedItems[0].Data) != `{"type":"thinking","thinking":"think\nexactly ☃","signature":"sig+/= exact"}` {
		t.Errorf("thinking item = %#v", first.CompletedItems[0])
	}
	if first.CompletedItems[1].OutputIndex == nil || *first.CompletedItems[1].OutputIndex != 2 || first.CompletedItems[1].Kind != "text" ||
		string(first.CompletedItems[1].Data) != `{"type":"text","text":"answer"}` {
		t.Errorf("text item = %#v", first.CompletedItems[1])
	}
	if first.CompletedItems[2].OutputIndex == nil || *first.CompletedItems[2].OutputIndex != 3 || first.CompletedItems[2].Kind != "redacted_thinking" ||
		string(first.CompletedItems[2].Data) != `{"type":"redacted_thinking","data":"opaque+/= bytes"}` {
		t.Errorf("redacted item = %#v", first.CompletedItems[2])
	}

	request := llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "answer", ContentOutputIndex: llm.OutputPosition(2), ToolCalls: []llm.ToolCall{{ID: "toolu_1", OutputIndex: llm.OutputPosition(0), Function: llm.ToolCallFunction{Name: "echo", Arguments: `{"x":1}`}}}, ContinuationItems: first.CompletedItems},
		{Role: llm.RoleTool, ToolCallID: "toolu_1", Content: "done"},
	}}
	for _, completeErr := range provider.Complete(t.Context(), request) {
		if completeErr != nil {
			t.Fatalf("replay Complete: %v", completeErr)
		}
	}
	body := <-requests
	blocks := messageContent(t, body["messages"].([]any)[1])
	if len(blocks) != 4 {
		t.Fatalf("replayed blocks = %#v", blocks)
	}
	assertBlock := func(index int, want map[string]any) {
		t.Helper()
		for key, value := range want {
			if blocks[index][key] != value {
				t.Errorf("block %d %s = %#v, want %#v (block %#v)", index, key, blocks[index][key], value, blocks[index])
			}
		}
	}
	assertBlock(0, map[string]any{"type": "tool_use", "id": "toolu_1", "name": "echo"})
	assertBlock(1, map[string]any{"type": "thinking", "thinking": "think\nexactly ☃", "signature": "sig+/= exact"})
	assertBlock(2, map[string]any{"type": "text", "text": "answer"})
	assertBlock(3, map[string]any{"type": "redacted_thinking", "data": "opaque+/= bytes"})
}

func TestProviderIgnoresForeignContinuationAndDoesNotSynthesizeReasoning(t *testing.T) {
	body := anthropicRequestBody(t, llm.CompletionRequest{Messages: []llm.Message{{
		Role:             llm.RoleAssistant,
		Content:          "visible",
		ReasoningContent: "unsigned reasoning",
		ContinuationItems: []llm.ContinuationItem{{
			OutputIndex: llm.OutputPosition(0), Provider: "other", Format: "content_block.v1", Kind: "thinking",
			Data: []byte(`{"type":"thinking","thinking":"bad","signature":"bad"}`),
		}},
	}}})
	blocks := messageContent(t, body["messages"].([]any)[0])
	if len(blocks) != 1 || blocks[0]["type"] != "text" || blocks[0]["text"] != "visible" {
		t.Fatalf("assistant blocks = %#v", blocks)
	}
}

func TestProviderAppliesStableThinkingDisplayOption(t *testing.T) {
	body := anthropicRequestBody(t, llm.CompletionRequest{
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "think"}},
		Thinking:  llm.ThinkingConfig{Enabled: true, BudgetTokens: 2048},
		Options:   llm.ModelOptions{"anthropic_thinking_display": "omitted"},
		MaxTokens: 4096,
	})
	thinking := body["thinking"].(map[string]any)
	if thinking["display"] != "omitted" {
		t.Fatalf("thinking display = %#v, want omitted", thinking["display"])
	}
}

func TestProviderStreamingCapturesCompletedNativeBlocksWithoutDuplicateThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeAnthropicEvent(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`)
		writeAnthropicEvent(w, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`)
		writeAnthropicEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"native thought"}}`)
		writeAnthropicEvent(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"stream-signature"}}`)
		writeAnthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeAnthropicEvent(w, "content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-stream"}}`)
		writeAnthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":1}`)
		writeAnthropicEvent(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`)
		writeAnthropicEvent(w, "message_stop", `{"type":"message_stop"}`)
	}))
	t.Cleanup(server.Close)
	provider, err := anthropic.NewProvider("test-key", anthropic.WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	var thinking strings.Builder
	var items []llm.ContinuationItem
	for chunk, completeErr := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}}) {
		if completeErr != nil {
			t.Fatalf("Complete: %v", completeErr)
		}
		thinking.WriteString(chunk.Thinking)
		items = append(items, chunk.CompletedItems...)
	}
	if thinking.String() != "native thought" {
		t.Errorf("thinking = %q, want one visible delta", thinking.String())
	}
	if len(items) != 2 || items[0].OutputIndex == nil || *items[0].OutputIndex != 0 || items[1].OutputIndex == nil || *items[1].OutputIndex != 1 {
		t.Fatalf("completed items = %#v", items)
	}
	if string(items[0].Data) != `{"type":"thinking","thinking":"native thought","signature":"stream-signature"}` ||
		string(items[1].Data) != `{"type":"redacted_thinking","data":"opaque-stream"}` {
		t.Errorf("completed item data = %q, %q", items[0].Data, items[1].Data)
	}
}
