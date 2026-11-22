package litellm_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// Tagged LiteLLM v1.109.0 forms: anthropic/chat/handler.py emits thinking
// ordinals (not content positions), including signature-only fragments and
// mirrored provider_specific_fields. factory.py replays top-level blocks.
const chatSignedBlocks = `[{"type":"thinking","thinking":"first","signature":"sig+/="},{"type":"thinking","thinking":"second","signature":"sig2"},{"type":"redacted_thinking","data":"opaque+/="}]`
const chatExactArguments = `{"number":9007199254740993,"text":"hi"}`

func chatSignedMessage() string {
	return `{"role":"assistant","function_call":null,"content":"visible","reasoning_content":"firstsecond","thinking_blocks":` + chatSignedBlocks + `,"provider_specific_fields":{"thinking_blocks":` + chatSignedBlocks + `,"citations":null},"tool_calls":[{"id":"real_call","type":"function","function":{"name":"echo","arguments":` + strconvChatString(chatExactArguments) + `}}]}`
}

func strconvChatString(s string) string { data, _ := json.Marshal(s); return string(data) }

func chatResponse(message, usage string) string {
	return `{"id":"chat_test","object":"chat.completion","choices":[{"index":0,"message":` + message + `,"finish_reason":"tool_calls"}],"usage":` + usage + `}`
}

func chatEvent(delta, finish string) string {
	return `{"id":"chat_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + strconvChatString(finish) + `}]}`
}

func chatSignedEvents() []string {
	block := `[{"type":"thinking","thinking":"fir","index":0}]`
	return []string{
		chatEvent(`{"role":"assistant","function_call":null,"audio":null,"reasoning_content":"fir","thinking_blocks":`+block+`,"provider_specific_fields":{"thinking_blocks":`+block+`}}`, ""),
		chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"st","index":0}]}`, ""),
		chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"sig+/=","index":0}]}`, ""),
		chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"second","index":1}]}`, ""),
		chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"sig2","index":1}]}`, ""),
		chatEvent(`{"thinking_blocks":[{"type":"redacted_thinking","data":"opaque+/=","index":2}]}`, ""),
		chatEvent(`{"content":"visible"}`, ""),
		chatEvent(`{"tool_calls":[{"index":0,"id":"real_call","type":"function","function":{"name":"echo","arguments":"{\"number\":9007199254740993,"}}]}`, ""),
		chatEvent(`{"tool_calls":[{"index":0,"function":{"arguments":"\"text\":\"hi\"}"}}]}`, "tool_calls"),
		`{"id":"chat_test","choices":[],"usage":{"prompt_tokens":600,"completion_tokens":10,"total_tokens":610}}`,
		"[DONE]",
	}
}

func serveChatEvents(w http.ResponseWriter, events []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
	}
}

func collectChat(t *testing.T, provider llm.Provider, req llm.CompletionRequest) (llm.Message, llm.CompletionChunk, error) {
	t.Helper()
	message := llm.Message{Role: llm.RoleAssistant}
	var metadata llm.CompletionChunk
	for chunk, err := range provider.Complete(t.Context(), req) {
		if err != nil {
			return message, metadata, err
		}
		message.Content += chunk.Content
		message.ReasoningContent += chunk.Thinking
		for _, item := range chunk.CompletedItems {
			message.ContinuationItems = append(message.ContinuationItems, item.Clone())
		}
		for _, call := range chunk.ToolCalls {
			index := slices.IndexFunc(message.ToolCalls, func(prior llm.ToolCall) bool { return prior.ID == call.ID })
			if index < 0 {
				message.ToolCalls = append(message.ToolCalls, call.Clone())
				continue
			}
			if call.Function.Name != "" {
				message.ToolCalls[index].Function.Name = call.Function.Name
			}
			message.ToolCalls[index].Function.Arguments += call.Function.Arguments
		}
		if chunk.UsageReported {
			metadata = chunk.Clone()
		}
	}
	return message, metadata, nil
}

func TestChatSignedTranslatedToolLoop(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode: %v", err)
				}
				requests <- body
				if stream {
					serveChatEvents(w, chatSignedEvents())
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(chatSignedMessage(), `{"prompt_tokens":600,"completion_tokens":10,"total_tokens":610}`))
			}))
			t.Cleanup(server.Close)
			provider := litellm.NewProvider("test-key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias))
			first, meta, err := collectChat(t, provider, llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}})
			if err != nil {
				t.Fatal(err)
			}
			<-requests
			if first.Content != "visible" || first.ReasoningContent != "firstsecond" || len(first.ContinuationItems) != 1 || len(first.ToolCalls) != 1 || first.ToolCalls[0].Function.Arguments != chatExactArguments || meta.Usage.PromptTokens != 600 {
				t.Fatalf("first=%+v meta=%+v", first, meta)
			}
			item := first.ContinuationItems[0]
			if item.Provider != "litellm" || item.Format != "translated_chat.v1" || item.OutputIndex != nil || strings.Contains(string(item.Data), server.URL) || strings.Contains(string(item.Data), "test-key") {
				t.Fatalf("envelope=%+v", item)
			}
			before := first.Clone()
			_, _, err = collectChat(t, provider, llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}, first, {Role: llm.RoleTool, ToolCallID: "real_call", Content: "result"}}})
			if err != nil {
				t.Fatal(err)
			}
			body := <-requests
			assistant := body["messages"].([]any)[1].(map[string]any)
			blocks := assistant["thinking_blocks"].([]any)
			if len(blocks) != 3 || blocks[0].(map[string]any)["signature"] != "sig+/=" || blocks[2].(map[string]any)["data"] != "opaque+/=" {
				t.Fatalf("blocks=%v", blocks)
			}
			for _, block := range blocks {
				if _, ok := block.(map[string]any)["index"]; ok {
					t.Fatal("thinking ordinal replayed as index")
				}
			}
			got, _ := json.Marshal(first)
			want, _ := json.Marshal(before)
			if string(got) != string(want) {
				t.Fatal("borrowed history mutated")
			}
		})
	}
}

func TestChatHistoryBindingsRejectLazilyBeforeIO(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(chatSignedMessage(), `{}`))
	}))
	t.Cleanup(server.Close)
	provider := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias))
	first, _, err := collectChat(t, provider, llm.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name   string
		mutate func(*llm.CompletionRequest)
	}{
		{"role", func(r *llm.CompletionRequest) { r.Messages[0].Role = llm.RoleUser }},
		{"visible", func(r *llm.CompletionRequest) { r.Messages[0].Content += "changed" }},
		{"thinking", func(r *llm.CompletionRequest) { r.Messages[0].ReasoningContent = "changed" }},
		{"id", func(r *llm.CompletionRequest) { r.Messages[0].ToolCalls[0].ID = "forged" }},
		{"name", func(r *llm.CompletionRequest) { r.Messages[0].ToolCalls[0].Function.Name = "forged" }},
		{"exact arguments", func(r *llm.CompletionRequest) {
			r.Messages[0].ToolCalls[0].Function.Arguments = `{"number":9007199254740992,"text":"hi"}`
		}},
		{"result association", func(r *llm.CompletionRequest) { r.Messages[1].ToolCallID = "other" }},
		{"missing result", func(r *llm.CompletionRequest) { r.Messages = r.Messages[:1] }},
		{"duplicate result", func(r *llm.CompletionRequest) { r.Messages = append(r.Messages, r.Messages[1]) }},
		{"duplicate identity", func(r *llm.CompletionRequest) { r.Messages = append(r.Messages, r.Messages[0], r.Messages[1]) }},
		{"native order", func(r *llm.CompletionRequest) { r.Messages[0].ContentOutputIndex = llm.OutputPosition(0) }},
		{"ordinal position", func(r *llm.CompletionRequest) { r.Messages[0].ContinuationItems[0].OutputIndex = llm.OutputPosition(0) }},
		{"unknown envelope", func(r *llm.CompletionRequest) {
			r.Messages[0].ContinuationItems[0].Data = bytesChatReplace(r.Messages[0].ContinuationItems[0].Data, `"version":1`, `"version":2`)
		}},
		{"format", func(r *llm.CompletionRequest) { r.Messages[0].ContinuationItems[0].Format = "content_block.v1" }},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			req := llm.CompletionRequest{Messages: []llm.Message{first.Clone(), {Role: llm.RoleTool, ToolCallID: "real_call", Content: "done"}}}
			tt.mutate(&req)
			stream := provider.Complete(t.Context(), req)
			if calls.Load() != 1 {
				t.Fatal("Complete was not lazy")
			}
			for _, err := range stream {
				if !errors.Is(err, litellm.ErrCompletion) {
					t.Fatalf("error=%v", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("invalid binding dispatched")
			}
		})
	}
	for _, other := range []*litellm.Provider{litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias)), litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithModel("other")), litellm.NewProvider("other-key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias)), litellm.NewProvider("key", litellm.WithBaseURL(server.URL+"/other"), litellm.WithModel(alias))} {
		_, _, err = collectChat(t, other, llm.CompletionRequest{Messages: []llm.Message{first.Clone(), {Role: llm.RoleTool, ToolCallID: "real_call"}}})
		if !errors.Is(err, litellm.ErrCompletion) {
			t.Fatalf("scope error=%v", err)
		}
	}
	for _, protocol := range []llm.ModelProtocol{llm.ModelProtocols.PROTOCOLMESSAGES, llm.ModelProtocols.PROTOCOLRESPONSES, llm.ModelProtocols.PROTOCOLGENERATECONTENT} {
		other := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias), litellm.WithProtocol(protocol))
		_, _, err = collectChat(t, other, llm.CompletionRequest{Messages: []llm.Message{first.Clone(), {Role: llm.RoleTool, ToolCallID: "real_call"}}})
		if !errors.Is(err, litellm.ErrCompletion) {
			t.Fatalf("protocol mismatch=%v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("scope mismatch dispatched")
	}
}

func bytesChatReplace(data []byte, old, replacement string) []byte {
	return []byte(strings.ReplaceAll(string(data), old, replacement))
}

func TestChatUnsupportedAndPartialStateFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		events []string
	}{
		{"conflicting mirrors", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"a","index":0}],"provider_specific_fields":{"thinking_blocks":[{"type":"thinking","thinking":"b","index":0}]}}`, "")}},
		{"mirror kind", []string{chatEvent(`{"thinking_blocks":[{"type":"compaction","index":0}]}`, "")}},
		{"unsigned partial", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","index":0}]}`, "stop")}},
		{"interrupted", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","index":0}]}`, "")}},
		{"signed but no finish", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","signature":"s","index":0}]}`, "")}},
		{"after visible", []string{chatEvent(`{"content":"hi"}`, ""), chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","signature":"s","index":0}]}`, "stop")}},
		{"missing kind", []string{chatEvent(`{"thinking_blocks":[{"thinking":"a","signature":"s","index":0}]}`, "stop")}},
		{"duplicate signature key", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"a","signature":"one","signature":"two","index":0}]}`, "stop")}},
		{"populated legacy function", []string{chatEvent(`{"function_call":{"name":"echo","arguments":"{}"}}`, "stop")}},
		{"missing ordinal", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","signature":"s"}]}`, "stop")}},
		{"ordinal gap", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","signature":"s","index":2}]}`, "stop")}},
		{"repeated signature", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private","signature":"s","index":0}]}`, ""), chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"s","index":0}]}`, "stop")}},
		{"reasoning conflict", []string{chatEvent(`{"reasoning_content":"other","thinking_blocks":[{"type":"thinking","thinking":"private","signature":"s","index":0}]}`, "stop")}},
		{"server tool", []string{chatEvent(`{"provider_specific_fields":{"web_search_results":[{"secret":"private"}]}}`, "stop")}},
		{"compaction", []string{chatEvent(`{"provider_specific_fields":{"compaction_blocks":[{"type":"compaction"}]}}`, "stop")}},
		{"case message content", []string{chatEvent(`{"Content":"case-private-canary"}`, "stop")}},
		{"case message role", []string{chatEvent(`{"Role":"assistant"}`, "stop")}},
		{"case message blocks", []string{chatEvent(`{"Thinking_Blocks":[{"type":"thinking","thinking":"a","signature":"s","index":0}]}`, "stop")}},
		{"unknown block field", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"a","signature":"s","index":0,"unknown":true}]}`, "stop")}},
		{"missing real id", []string{chatEvent(`{"tool_calls":[{"index":0,"type":"function","function":{"name":"echo","arguments":"{}"}}]}`, "tool_calls")}},
		{"duplicate id", []string{chatEvent(`{"tool_calls":[{"index":0,"id":"same","type":"function","function":{"name":"echo","arguments":"{}"}},{"index":1,"id":"same","type":"function","function":{"name":"echo","arguments":"{}"}}]}`, "tool_calls")}},
		{"partial args", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"a","signature":"s","index":0}],"tool_calls":[{"index":0,"id":"real","type":"function","function":{"name":"echo","arguments":"{"}}]}`, "tool_calls")}},
		{"length partial", []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"a","signature":"s","index":0}]}`, "length")}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { serveChatEvents(w, tt.events) }))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("private-key", litellm.WithBaseURL(server.URL))
			message, _, err := collectChat(t, p, llm.CompletionRequest{Stream: true})
			if !errors.Is(err, litellm.ErrCompletion) || len(message.ContinuationItems) != 0 || strings.Contains(err.Error(), "private") {
				t.Fatalf("error=%v items=%v", err, message.ContinuationItems)
			}
		})
	}
}

func TestChatDialectIsExplicitNotURLOrAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(chatSignedMessage(), `{}`))
	}))
	t.Cleanup(server.Close)
	p := openai.NewProvider("key", openai.WithBaseURL(server.URL), openai.WithModel("claude-looks-native"))
	message, _, err := collectChat(t, p, llm.CompletionRequest{})
	if err != nil || len(message.ContinuationItems) != 0 {
		t.Fatalf("direct OpenAI changed: %v %v", err, message.ContinuationItems)
	}
}

func TestChatNonstreamUnsupportedSignedEnvelopes(t *testing.T) {
	messages := []string{
		`{"role":"user","thinking_blocks":[{"type":"thinking","thinking":"a","signature":"s"}]}`,
		`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"a"}]}`,
		`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"a","signature":"s"}],"provider_specific_fields":{"thinking_blocks":[{"type":"thinking","thinking":"a","signature":"conflict"}]}}`,
		`{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque","index":0}]}`,
		`{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}],"provider_specific_fields":{"tool_results":[{}]}}`,
	}
	for i, message := range messages {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(message, `{}`))
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
			got, _, err := collectChat(t, p, llm.CompletionRequest{})
			if !errors.Is(err, litellm.ErrCompletion) || len(got.ContinuationItems) != 0 {
				t.Fatalf("error=%v items=%v", err, got.ContinuationItems)
			}
		})
	}
}

func TestChatCompletePrefixIsNotReplayableAfterTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"complete","signature":"s","index":0}],"content":"answer"}`, "stop"), `{"error":{"message":"unsafe diagnostics"}}`})
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
	message, _, err := collectChat(t, p, llm.CompletionRequest{Stream: true})
	if !errors.Is(err, litellm.ErrCompletion) || len(message.ContinuationItems) != 0 || message.Content != "answer" || message.ReasoningContent != "complete" {
		t.Fatalf("message=%+v error=%v", message, err)
	}
}

func TestChatSignatureOnlyThinkingRetainsRequiredEmptyText(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			requests := make(chan map[string]any, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode: %v", err)
				}
				requests <- body
				if stream {
					serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"signature-only","index":0}]}`, "stop"), "[DONE]"})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"","signature":"signature-only"}]}`, `{}`))
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
			message, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
			if err != nil {
				t.Fatal(err)
			}
			<-requests
			if len(message.ContinuationItems) != 1 || message.ReasoningContent != "" {
				t.Fatalf("message=%+v", message)
			}
			_, _, err = collectChat(t, p, llm.CompletionRequest{Stream: stream, Messages: []llm.Message{message}})
			if err != nil {
				t.Fatal(err)
			}
			body := <-requests
			block := body["messages"].([]any)[0].(map[string]any)["thinking_blocks"].([]any)[0].(map[string]any)
			thinking, present := block["thinking"]
			if !present || thinking != "" || block["signature"] != "signature-only" {
				t.Fatalf("required signed fields=%v", block)
			}
		})
	}
}

func TestChatOwnedShapesRequireExactNamesWithoutFoldingArguments(t *testing.T) {
	const block = `{"type":"thinking","thinking":"shape-private-canary","signature":"shape-signature-canary","cache_control":{"type":"ephemeral","ttl":"5m"}}`
	const call = `{"id":"real","type":"function","function":{"name":"echo","arguments":"{\"a\":1,\"A\":2}"}}`
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[`+block+`],"tool_calls":[`+call+`]}`, `{}`))
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(openai.LiteLLMPromptCachePolicy{AllowReplayMarkers: true}))
	first, _, err := collectChat(t, p, llm.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "scope", "content", "thinking_blocks", "tool_calls", "type", "thinking", "signature", "cache_control", "ttl", "name", "arguments"} {
		t.Run("stored/"+field, func(t *testing.T) {
			message := first.Clone()
			raw := string(message.ContinuationItems[0].Data)
			// Add a conflicting case-variant key as well as testing replacement below.
			changed := strings.Replace(raw, `"`+field+`":`, `"`+strings.ToUpper(field)+`":`, 1)
			if raw == changed {
				t.Fatalf("fixture missing %s", field)
			}
			duplicate := strings.Replace(raw, `"`+field+`":`, `"`+strings.ToUpper(field)+`":null,"`+field+`":`, 1)
			for _, mutation := range []string{changed, duplicate} {
				message.ContinuationItems[0].Data = []byte(mutation)
				_, _, err := collectChat(t, p, llm.CompletionRequest{Messages: []llm.Message{message, {Role: llm.RoleTool, ToolCallID: "real", Content: "done"}}})
				if !errors.Is(err, litellm.ErrCompletion) || calls.Load() != 1 {
					t.Fatalf("case-variant dispatched: %v calls=%d", err, calls.Load())
				}
				if strings.Contains(err.Error(), "shape-private-canary") || strings.Contains(err.Error(), "shape-signature-canary") {
					t.Fatal("private diagnostic")
				}
			}
		})
	}
	// Arbitrary argument keys a/A are distinct and retain their exact bytes.
	_, _, err = collectChat(t, p, llm.CompletionRequest{Messages: []llm.Message{first, {Role: llm.RoleTool, ToolCallID: "real", Content: "done"}}})
	if err != nil || calls.Load() != 2 {
		t.Fatalf("argument keys folded: %v", err)
	}
	for _, stream := range []bool{false, true} {
		for _, bad := range []string{
			strings.Replace(block, `"type":`, `"Type":`, 1),
			strings.Replace(block, `"thinking":`, `"Thinking":`, 1),
			strings.Replace(block, `"signature":"shape-signature-canary"`, `"signature":"first","Signature":"shape-signature-canary"`, 1),
			strings.Replace(block, `"cache_control":`, `"Cache_Control":`, 1),
			strings.Replace(block, `"ttl":`, `"TTL":`, 1),
			strings.Replace(block, `"type":"ephemeral"`, `"Type":"ephemeral"`, 1),
			strings.Replace(block, `"signature":`, `"unsupported":true,"signature":`, 1),
		} {
			t.Run(fmt.Sprintf("response/stream=%v/%s", stream, bad[:24]), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if stream {
						wire := strings.Replace(bad, `"shape-private-canary"`, `"shape-private-canary","index":0`, 1)
						serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[`+wire+`]}`, "stop"), "[DONE]"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[`+bad+`]}`, `{}`))
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(openai.LiteLLMPromptCachePolicy{AllowReplayMarkers: true}))
				got, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if !errors.Is(err, litellm.ErrCompletion) || len(got.ContinuationItems) != 0 {
					t.Fatalf("owned shape accepted: %v", err)
				}
				if strings.Contains(err.Error(), "shape-private-canary") || strings.Contains(err.Error(), "shape-signature-canary") {
					t.Fatal("private diagnostic")
				}
			})
		}
	}
}

func TestChatScopeIsRandomConstructorLocalAndConcurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}]}`, `{}`))
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("scope-key-canary", litellm.WithBaseURL(server.URL), litellm.WithModel("alias"))
	type result struct {
		scope string
		err   error
	}
	results := make(chan result, 8) // One bounded result per joined invocation.
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			message, _, err := collectChat(t, p, llm.CompletionRequest{})
			if err != nil {
				results <- result{err: err}
				return
			}
			var envelope struct {
				Scope string `json:"scope"`
			}
			err = json.Unmarshal(message.ContinuationItems[0].Data, &envelope)
			results <- result{scope: envelope.Scope, err: err}
		})
	}
	group.Wait()
	close(results)
	stable := ""
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.scope) != 64 {
			t.Fatal("missing random scope")
		}
		if stable != "" && got.scope != stable {
			t.Fatal("scope changed within concrete gateway provider")
		}
		stable = got.scope
	}
	seen := map[string]bool{stable: true}
	for range 8 {
		fresh := litellm.NewProvider("scope-key-canary", litellm.WithBaseURL(server.URL), litellm.WithModel("alias"))
		message, _, err := collectChat(t, fresh, llm.CompletionRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Scope string `json:"scope"`
		}
		if err := json.Unmarshal(message.ContinuationItems[0].Data, &envelope); err != nil {
			t.Fatal(err)
		}
		if seen[envelope.Scope] {
			t.Fatal("scope is a deterministic credential verifier")
		}
		seen[envelope.Scope] = true
		if strings.Contains(string(message.ContinuationItems[0].Data), "scope-key-canary") {
			t.Fatal("credential in envelope")
		}
	}
}
