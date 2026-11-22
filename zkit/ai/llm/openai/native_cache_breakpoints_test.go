package openai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func nativePolicyInput(t *testing.T, body map[string]any, responses bool) []any {
	t.Helper()
	key := "messages"
	if responses {
		key = "input"
	}
	items, ok := body[key].([]any)
	if !ok {
		t.Fatalf("missing %s", key)
	}
	return items
}

// Remove only the policy's permitted wire deltas before comparing with the
// legacy request. Opaque nested native data is deliberately not traversed.
func nativePolicyCompareUnmarked(t *testing.T, modern, legacy []any) {
	t.Helper()
	if len(modern) != len(legacy) {
		t.Fatalf("item count changed: %d -> %d", len(legacy), len(modern))
	}
	for index, item := range modern {
		message := item.(map[string]any)
		old := legacy[index].(map[string]any)
		if content, ok := message["content"].([]any); ok {
			for _, raw := range content {
				delete(raw.(map[string]any), "prompt_cache_breakpoint")
			}
			if scalar, ok := old["content"].(string); ok {
				if len(content) != 1 || content[0].(map[string]any)["text"] != scalar || content[0].(map[string]any)["type"] != "text" {
					t.Fatalf("scalar conversion changed text: %#v", message)
				}
				message["content"] = scalar
			}
		}
	}
	if !reflect.DeepEqual(modern, legacy) {
		t.Errorf("unselected wire data changed\nmodern: %#v\nlegacy: %#v", modern, legacy)
	}
}

func TestNativeCacheBreakpointsSourceProvenance(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "repeat"},
		{Role: llm.RoleUser, Content: "ignored scalar", Parts: []llm.ContentPart{{Type: llm.ContentTypeImage}, llm.TextPart("repeat"), llm.ImagePartFromURL("https://image.invalid/user"), llm.TextPart(""), llm.TextPart("repeat")}},
		{Role: llm.RoleAssistant, Content: "assistant", ReasoningContent: "drop reasoning", ToolCalls: []llm.ToolCall{{ID: "a", Function: llm.ToolCallFunction{Name: "first", Arguments: `{"x":1}`}}, {ID: "b", Function: llm.ToolCallFunction{Name: "second", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "a", Content: "tool one", Parts: []llm.ContentPart{llm.ImagePartFromURL("https://image.invalid/a"), llm.TextPart("repeat")}},
		{Role: llm.RoleTool, ToolCallID: "b", Content: "tool two", Parts: []llm.ContentPart{llm.ImagePartFromURL("https://image.invalid/b")}},
		{Role: llm.RoleAssistant, Content: "after tools", ReasoningContent: "keep reasoning"},
		{Role: llm.RoleUser, Content: "repeat"},
		{Role: llm.RoleUser, Content: "ignored", Parts: []llm.ContentPart{llm.TextPart("repeat")}},
	}
	for _, responses := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(map[bool]string{false: "chat", true: "responses"}[responses]+map[bool]string{false: "/JSON", true: "/SSE"}[stream], func(t *testing.T) {
				var captured []map[string]any
				client, _, _ := nativePolicyClient(t, func(_ *http.Request, body map[string]any) { captured = append(captured, body) })
				maskCalls := 0
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(responses), openai.WithReasoningHistory(llm.ReasoningHistories.FIELD), openai.WithReasoningKeepMask(func(canonical []llm.Message) []bool {
					maskCalls++
					if !reflect.DeepEqual(canonical, messages) {
						t.Error("keep mask did not receive original canonical history")
					}
					return []bool{true, true, false, true, true, true, true, true}
				}))
				req := llm.CompletionRequest{Messages: messages, Stream: stream}
				before := llm.CloneMessages(messages)
				nativePolicyDrain(t, provider.Complete(t.Context(), req))
				ttl := "30m"
				if responses {
					ttl = "12h"
				}
				first := nativePolicySelector(0, 0)
				first["ttl"] = "30m"
				last := nativePolicySelector(7, 0)
				last["ttl"] = ttl
				req.Options = llm.ModelOptions{openai.PromptCacheOption: map[string]any{"mode": "explicit", "ttl": ttl, "breakpoints": []any{first, nativePolicySelector(1, 4), nativePolicySelector(6, 0), last}}}
				optionsBefore := req.Options.Clone()
				nativePolicyDrain(t, provider.Complete(t.Context(), req))
				legacy := nativePolicyInput(t, captured[0], responses)
				modern := nativePolicyInput(t, captured[1], responses)
				positions := [][2]int{{0, 0}, {1, 3}, {7, 0}, {8, 0}}
				if responses {
					positions = [][2]int{{0, 0}, {1, 3}, {9, 0}, {10, 0}}
				}
				marked := 0
				for itemIndex, item := range modern {
					message := item.(map[string]any)
					content, _ := message["content"].([]any)
					for contentIndex, raw := range content {
						part := raw.(map[string]any)
						marker, exists := part["prompt_cache_breakpoint"]
						want := false
						for _, position := range positions {
							want = want || position == [2]int{itemIndex, contentIndex}
						}
						if exists != want {
							t.Errorf("marker at %d/%d = %v, want %v", itemIndex, contentIndex, exists, want)
						}
						if exists {
							marked++
							if part["text"] != "repeat" {
								t.Errorf("wrong selected text: %#v", part)
							}
							wantMarker := map[string]any{"mode": "explicit"}
							switch itemIndex {
							case 0:
								wantMarker["ttl"] = "30m"
							case positions[3][0]:
								wantMarker["ttl"] = ttl
							}
							if !reflect.DeepEqual(marker, wantMarker) {
								t.Errorf("marker=%#v, want %#v", marker, wantMarker)
							}
						}
					}
				}
				if marked != 4 {
					t.Errorf("marked %d blocks", marked)
				}
				nativePolicyCompareUnmarked(t, modern, legacy)
				if !responses && maskCalls != 2 {
					t.Errorf("keep mask calls=%d", maskCalls)
				}
				if !reflect.DeepEqual(messages, before) || !reflect.DeepEqual(req.Options, optionsBefore) {
					t.Error("borrowed input mutated")
				}
			})
		}
	}
}

func TestNativeCacheBreakpointsRejectUnsupportedSources(t *testing.T) {
	index := 0
	cases := []struct {
		name    string
		message llm.Message
		content int
	}{
		{"empty user", llm.Message{Role: llm.RoleUser}, 0},
		{"empty system", llm.Message{Role: llm.RoleSystem}, 0},
		{"assistant", llm.Message{Role: llm.RoleAssistant, Content: "secret-canary"}, 0},
		{"native assistant", llm.Message{Role: llm.RoleAssistant, Content: "secret-canary", ContinuationItems: []llm.ContinuationItem{{Provider: "openai", Format: "responses.output_item", Data: json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native"}]}`), OutputIndex: &index}}}, 0},
		{"tool", llm.Message{Role: llm.RoleTool, Content: "secret-canary", Parts: []llm.ContentPart{llm.TextPart("tool text")}}, 0},
		{"developer", llm.Message{Role: "developer", Content: "secret-canary"}, 0},
		{"unknown role", llm.Message{Role: "unknown", Content: "secret-canary"}, 0},
		{"empty role", llm.Message{Content: "secret-canary"}, 0},
		{"system parts", llm.Message{Role: llm.RoleSystem, Content: "scalar", Parts: []llm.ContentPart{llm.TextPart("secret-canary")}}, 0},
		{"user image", llm.Message{Role: llm.RoleUser, Content: "hidden text", Parts: []llm.ContentPart{llm.ImagePartFromURL("https://image.invalid/")}}, 0},
		{"empty text part", llm.Message{Role: llm.RoleUser, Content: "hidden text", Parts: []llm.ContentPart{llm.TextPart("")}}, 0},
		{"audio", llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeAudio}}}, 0},
		{"video", llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.VideoPartFromURL("https://video.invalid/")}}, 0},
		{"unknown part", llm.Message{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: "secret-canary"}}}, 0},
	}
	for _, responses := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name+map[bool]string{false: "/chat", true: "/responses"}[responses], func(t *testing.T) {
				client, calls, _ := nativePolicyClient(t, nil)
				provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(responses))
				req := nativePolicyRequest(map[string]any{"mode": "explicit", "breakpoints": []any{nativePolicySelector(0, tc.content)}})
				req.Messages = []llm.Message{tc.message}
				nativePolicyWantError(t, provider.Complete(t.Context(), req), openai.ErrInvalidCachePolicy)
				if calls.Load() != 0 {
					t.Error("invalid source reached HTTP")
				}
			})
		}
	}
}

func TestNativeCacheBreakpointsSlotLimits(t *testing.T) {
	for _, mode := range []string{"implicit", "explicit"} {
		for _, count := range []int{0, 3, 4, 5} {
			client, calls, _ := nativePolicyClient(t, nil)
			provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(false))
			selectors := make([]any, 0, count)
			req := llm.CompletionRequest{}
			for index := range count {
				selectors = append(selectors, nativePolicySelector(index, 0))
				req.Messages = append(req.Messages, llm.Message{Role: llm.RoleUser, Content: "repeat"})
			}
			req.Options = llm.ModelOptions{openai.PromptCacheOption: map[string]any{"mode": mode, "breakpoints": selectors}}
			if count > 4 || mode == "implicit" && count > 3 {
				nativePolicyWantError(t, provider.Complete(t.Context(), req), openai.ErrInvalidCachePolicy)
				if calls.Load() != 0 {
					t.Error("slot overflow reached HTTP")
				}
			} else {
				nativePolicyDrain(t, provider.Complete(t.Context(), req))
			}
		}
	}
}

func TestNativeCacheBreakpointsPreserveOpaqueNativeAuthority(t *testing.T) {
	nativeIndex, reasoningIndex, toolIndex := 2, 0, 1
	rawText := json.RawMessage(`{"type":"message","role":"assistant","id":"native","content":[{"type":"output_text","text":"native authoritative","annotations":[{"nested":{"prompt_cache_breakpoint":"opaque annotation-canary"}}]}],"opaque":{"prompt_cache_breakpoint":"opaque schema-canary"}}`)
	rawReasoning := json.RawMessage(`{"type":"reasoning","id":"reasoning","encrypted_content":"signed-canary","summary":[{"type":"summary_text","text":"opaque","prompt_cache_breakpoint":"summary-canary"}]}`)
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: "must not duplicate", ReasoningContent: "display only", ContinuationItems: []llm.ContinuationItem{
			{Provider: "openai", Format: "responses.output_item", Data: rawText, OutputIndex: &nativeIndex},
			{Provider: "openai", Format: "responses.output_item", Data: rawReasoning, OutputIndex: &reasoningIndex},
		}, ToolCalls: []llm.ToolCall{{ID: "call", OutputIndex: &toolIndex, Function: llm.ToolCallFunction{Name: "tool", Arguments: `{"prompt_cache_breakpoint":"opaque tool-canary"}`}}}},
		{Role: llm.RoleUser, Content: "selected"},
	}
	for _, stream := range []bool{false, true} {
		var bodies []map[string]any
		var rawInputs [][]json.RawMessage
		client, _, _ := nativePolicyClient(t, func(request *http.Request, body map[string]any) {
			bodies = append(bodies, body)
			copyBody, err := request.GetBody()
			if err != nil {
				t.Fatal(err)
			}
			defer copyBody.Close()
			var wire struct {
				Input []json.RawMessage `json:"input"`
			}
			if err := json.NewDecoder(copyBody).Decode(&wire); err != nil {
				t.Fatal(err)
			}
			rawInputs = append(rawInputs, wire.Input)
		})
		provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true))
		req := llm.CompletionRequest{Messages: messages, Stream: stream}
		before := llm.CloneMessages(messages)
		nativePolicyDrain(t, provider.Complete(t.Context(), req))
		req.Options = llm.ModelOptions{openai.PromptCacheOption: map[string]any{"mode": "implicit", "breakpoints": []any{nativePolicySelector(1, 0)}}}
		nativePolicyDrain(t, provider.Complete(t.Context(), req))
		legacy := nativePolicyInput(t, bodies[0], true)
		modern := nativePolicyInput(t, bodies[1], true)
		if len(modern) != 4 {
			t.Fatalf("native assistant duplicated: %#v", modern)
		}
		for index, want := range []string{"reasoning", "function_call", "message", "message"} {
			if modern[index].(map[string]any)["type"] != want {
				t.Errorf("item %d reordered: %#v", index, modern[index])
			}
		}
		if modern[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "native authoritative" {
			t.Error("native authority lost")
		}
		for _, position := range []int{0, 1, 2} {
			if !bytes.Equal(rawInputs[0][position], rawInputs[1][position]) {
				t.Errorf("opaque native bytes changed at %d", position)
			}
		}
		if !bytes.Equal(rawInputs[1][0], rawReasoning) || !bytes.Equal(rawInputs[1][2], rawText) {
			t.Error("opaque native JSON was reserialized")
		}
		nativePolicyCompareUnmarked(t, modern, legacy)
		if !reflect.DeepEqual(messages, before) {
			t.Error("signed native items mutated")
		}
	}
}

func TestNativeCacheBreakpointsRejectAuthoritativeReplayMarkers(t *testing.T) {
	index := 0
	cases := []string{
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native","prompt_cache_breakpoint":{"mode":"explicit"}}]}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native","prompt_cache_breakpoint":"secret-canary"}]}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native","Prompt_Cache_Breakpoint":null}]}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native","prompt_cache_\u0062reakpoint":false}]}`,
		`{"type":"message","role":"assistant","Content":[{"type":"output_text","text":"native","PROMPT_CACHE_BREAKPOINT":{}}]}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first","prompt_cache_breakpoint":{}}],"content":[{"type":"output_text","text":"native"}]}`,
		`{"type":"reasoning","encrypted_content":"signed-canary","content":[{"type":"output_text","text":"native","prompt_cache_breakpoint":null}]}`,
	}
	for _, raw := range cases {
		var bodies []map[string]any
		client, calls, _ := nativePolicyClient(t, func(_ *http.Request, body map[string]any) { bodies = append(bodies, body) })
		provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true))
		req := llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "", ContinuationItems: []llm.ContinuationItem{{Provider: "openai", Format: "responses.output_item", Data: json.RawMessage(raw), OutputIndex: &index}}}}}
		nativePolicyDrain(t, provider.Complete(t.Context(), req)) // Legacy replays even malformed opaque marker values unchanged.
		if len(bodies) != 1 || len(nativePolicyInput(t, bodies[0], true)) != 1 {
			t.Fatal("legacy native item was ignored")
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(raw), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(nativePolicyInput(t, bodies[0], true)[0], want) {
			t.Fatal("legacy native marker item rewritten")
		}
		req.Options = llm.ModelOptions{openai.PromptCacheOption: map[string]any{"mode": "explicit"}}
		nativePolicyWantError(t, provider.Complete(t.Context(), req), openai.ErrInvalidCachePolicy)
		if calls.Load() != 1 {
			t.Errorf("marker conflict reached HTTP, calls=%d", calls.Load())
		}
	}
}

func TestNativeCacheBreakpointsIgnoreUnusedReplayMarkers(t *testing.T) {
	index := 0
	raw := json.RawMessage(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"unused","prompt_cache_breakpoint":{}}]}`)
	for _, item := range []llm.ContinuationItem{
		{Provider: "other", Format: "responses.output_item", Data: raw, OutputIndex: &index},
		{Provider: "openai", Format: "other", Data: raw, OutputIndex: &index},
		{Provider: "openai", Format: "responses.output_item", Data: raw}, // No index: not authoritative native text.
		{Provider: "openai", Format: "responses.output_item", Data: json.RawMessage(`{"type":"unknown","content":[{"prompt_cache_breakpoint":{}}]}`), OutputIndex: &index},
	} {
		client, _, _ := nativePolicyClient(t, nil)
		provider := openai.NewProvider("test", openai.WithHTTPClient(client), openai.WithResponsesAPI(true))
		req := nativePolicyRequest(map[string]any{"mode": "explicit"})
		req.Messages = []llm.Message{{Role: llm.RoleAssistant, Content: "fallback", ContinuationItems: []llm.ContinuationItem{item}}}
		nativePolicyDrain(t, provider.Complete(t.Context(), req))
	}
}
