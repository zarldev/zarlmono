package litellm_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Fixtures follow the public frontend contracts of LiteLLM v1.109.0,
// tag 55c9c6af17a9acc4b46e707e8761c4592d0e0954. They deliberately emulate
// native signed responses, not proof that an arbitrary Chat bridge retains them.
type protocolFixture struct {
	protocol    llm.ModelProtocol
	path        string
	first       string
	final       string
	events      []string
	finalEvents []string
}

const (
	alias             = "Company/Production-Alias-V7"
	signedReasoning   = `{"type":"reasoning","id":"rs_1","status":"completed","encrypted_content":"opaque-gateway-reasoning","summary":[{"type":"summary_text","text":"thinking"}]}`
	responseCall      = `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"echo","status":"completed","arguments":"{\"text\":\"hi\"}"}`
	anthropicThinking = `{"type":"thinking","thinking":"thinking","signature":"signed-gateway-thinking"}`
	googleParts       = `[{"text":"thinking","thought":true,"thoughtSignature":"dGhvdWdodA=="},{"functionCall":{"id":"call_1","name":"echo","args":{"text":"hi"}},"thoughtSignature":"Y2FsbA=="}]`
)

func fixtures() []protocolFixture {
	return []protocolFixture{
		{protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, path: "/v1/chat/completions",
			first:       `{"id":"chat_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
			final:       `{"id":"chat_2","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`,
			events:      []string{`{"id":"chat_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`, "[DONE]"},
			finalEvents: []string{`{"id":"chat_2","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`, "[DONE]"},
		},
		{protocol: llm.ModelProtocols.PROTOCOLRESPONSES, path: "/v1/responses",
			first:       `{"id":"resp_gateway_1","status":"completed","output":[` + signedReasoning + `,` + responseCall + `],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`,
			final:       `{"id":"resp_gateway_2","status":"completed","output":[{"type":"message","id":"msg_2","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}`,
			events:      []string{`{"type":"response.output_item.done","output_index":0,"item":` + signedReasoning + `}`, `{"type":"response.output_item.added","output_index":1,"item":` + responseCall + `}`, `{"type":"response.output_item.done","output_index":1,"item":` + responseCall + `}`, `{"type":"response.completed","response":{"id":"resp_gateway_1","status":"completed","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`},
			finalEvents: []string{`{"type":"response.output_text.delta","delta":"done"}`, `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}`},
		},
		{protocol: llm.ModelProtocols.PROTOCOLMESSAGES, path: "/v1/messages",
			first: `{"id":"msg_1","type":"message","role":"assistant","model":"ignored","content":[` + anthropicThinking + `,{"type":"tool_use","id":"call_1","name":"echo","input":{"text":"hi"}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`,
			final: `{"id":"msg_2","type":"message","role":"assistant","model":"ignored","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"output_tokens":2}}`,
			events: []string{
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"ignored","usage":{"input_tokens":5,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-gateway-thinking"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"echo","input":{}}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\"hi\"}"}}`,
				`{"type":"content_block_stop","index":1}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}`, `{"type":"message_stop"}`,
			},
			finalEvents: []string{
				`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","content":[],"model":"ignored","usage":{"input_tokens":8,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`, `{"type":"message_stop"}`,
			},
		},
		{protocol: llm.ModelProtocols.PROTOCOLGENERATECONTENT, path: "/v1beta/models/" + alias,
			first:       `{"candidates":[{"content":{"role":"model","parts":` + googleParts + `},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`,
			final:       `{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"totalTokenCount":10}}`,
			events:      []string{`{"candidates":[{"content":{"role":"model","parts":` + googleParts + `},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`},
			finalEvents: []string{`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2,"totalTokenCount":10}}`},
		},
	}
}

func (f protocolFixture) serve(w http.ResponseWriter, stream, final bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		body := f.first
		if final {
			body = f.final
		}
		_, _ = io.WriteString(w, body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	events := f.events
	if final {
		events = f.finalEvents
	}
	for _, data := range events {
		if f.protocol == llm.ModelProtocols.PROTOCOLMESSAGES {
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(data), &event)
			_, _ = fmt.Fprintf(w, "event: %s\n", event.Type)
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
}

func (f protocolFixture) requestPath(stream bool) string {
	if f.protocol != llm.ModelProtocols.PROTOCOLGENERATECONTENT {
		return f.path
	}
	if stream {
		return f.path + ":streamGenerateContent"
	}
	return f.path + ":generateContent"
}

func collect(t *testing.T, stream llm.CompletionStream) (llm.Message, llm.CompletionChunk) {
	t.Helper()
	assistant := llm.Message{Role: llm.RoleAssistant}
	var final llm.CompletionChunk
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		chunk = chunk.Clone()
		assistant.Content += chunk.Content
		assistant.ReasoningContent += chunk.Thinking
		assistant.ContinuationItems = append(assistant.ContinuationItems, chunk.CompletedItems...)
		assistant.ToolCalls = append(assistant.ToolCalls, chunk.ToolCalls...)
		if chunk.ContentOutputIndex != nil {
			assistant.ContentOutputIndex = chunk.ContentOutputIndex
		}
		if chunk.UsageReported || chunk.FinishReason != llm.FinishReasons.UNKNOWN {
			final = chunk
		}
	}
	return assistant, final
}
