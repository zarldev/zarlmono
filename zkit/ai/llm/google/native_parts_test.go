package google_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestNativePartsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		frames   []string
		parts    string
		content  string
		thinking string
		calls    int
	}{
		{
			name:    "signed text call text and parallel calls",
			frames:  []string{`[{"text":"before","thoughtSignature":"YmVmb3Jl"},{"functionCall":{"name":"read","args":{"path":"a","extension":{"nested":[true,2]}}},"thoughtSignature":"Y2FsbA=="},{"text":"after","thoughtSignature":"YWZ0ZXI="},{"functionCall":{"id":"native-write","name":"write","args":{"path":"b"}}}]`},
			parts:   `[{"text":"before","thoughtSignature":"YmVmb3Jl"},{"functionCall":{"name":"read","args":{"path":"a","extension":{"nested":[true,2]}}},"thoughtSignature":"Y2FsbA=="},{"text":"after","thoughtSignature":"YWZ0ZXI="},{"functionCall":{"id":"native-write","name":"write","args":{"path":"b"}}}]`,
			content: "beforeafter", calls: 2,
		},
		{
			name:   "signed parameterless call",
			frames: []string{`[{"functionCall":{"name":"read"},"thoughtSignature":"c2ln"}]`},
			parts:  `[{"functionCall":{"name":"read"},"thoughtSignature":"c2ln"}]`,
			calls:  1,
		},
		{
			name:    "thought and signature only fragments",
			frames:  []string{`[{"text":"private ","thought":true}]`, `[{"text":"thought","thought":true,"thoughtSignature":"dGhvdWdodA=="}]`, `[{"thoughtSignature":"c2lnbmF0dXJl"}]`, `[{"text":"visible"}]`},
			parts:   `[{"text":"private ","thought":true},{"text":"thought","thought":true,"thoughtSignature":"dGhvdWdodA=="},{"thoughtSignature":"c2lnbmF0dXJl"},{"text":"visible"}]`,
			content: "visible", thinking: "private thought",
		},
		{
			name:    "text fragments before signed call",
			frames:  []string{`[{"text":"one "}]`, `[{"text":"two"}]`, `[{"thoughtSignature":"c2ln"}]`, `[{"functionCall":{"id":"read-id","name":"read","args":{"path":"a"}},"thoughtSignature":"Y2FsbA=="}]`, `[{"text":"after"}]`},
			parts:   `[{"text":"one "},{"text":"two"},{"thoughtSignature":"c2ln"},{"functionCall":{"id":"read-id","name":"read","args":{"path":"a"}},"thoughtSignature":"Y2FsbA=="},{"text":"after"}]`,
			content: "one twoafter", calls: 1,
		},
		{
			// The SDK has already rounded incoming open numbers before the
			// adapter sees a typed Part. The outbound hook cannot recover them.
			name:   "SDK capture already rounds open numbers",
			frames: []string{`[{"functionCall":{"id":"call","name":"read","args":{"n":9007199254740993,"fraction":0.10000000000000001}},"partMetadata":{"n":9007199254740993},"thoughtSignature":"c2ln"}]`},
			parts:  `[{"functionCall":{"id":"call","name":"read","args":{"n":9007199254740992,"fraction":0.1}},"partMetadata":{"n":9007199254740992},"thoughtSignature":"c2ln"}]`,
			calls:  1,
		},
		{
			name:    "SDK open metadata retained but unknown top level lost",
			frames:  []string{`[{"text":"answer","thoughtSignature":"c2ln","partMetadata":{"future":{"array":[1,"two",true],"unknown":"retained"}},"unknownTopLevel":{"lost":true}}]`},
			parts:   `[{"text":"answer","thoughtSignature":"c2ln","partMetadata":{"future":{"array":[1,"two",true],"unknown":"retained"}}}]`,
			content: "answer",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, test.frames)
			assistant := collectNativeAssistant(t, provider, []llm.Message{{Role: llm.RoleUser, Content: "start"}})
			<-requests
			if assistant.Content != test.content || assistant.ReasoningContent != test.thinking || len(assistant.ToolCalls) != test.calls {
				t.Fatalf("display projection = %#v", assistant)
			}
			var wantParts []map[string]any
			decodeNativeTestJSON(t, []byte(test.parts), &wantParts)
			if len(assistant.ContinuationItems) != len(wantParts) {
				t.Fatalf("native items = %d, want %d", len(assistant.ContinuationItems), len(wantParts))
			}
			var exactParts []json.RawMessage
			decodeNativeTestJSON(t, []byte(test.parts), &exactParts)
			for index, item := range assistant.ContinuationItems {
				var want, got any
				decodeNativeTestJSON(t, exactParts[index], &want)
				decodeNativeTestJSON(t, item.Data, &got)
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("captured SDK part %d (-want +got):\n%s", index, diff)
				}
				if item.Provider != "google" || item.Format != "generate_content.part.v2" || item.OutputIndex == nil || *item.OutputIndex != index {
					t.Fatalf("native item %d = %#v", index, item)
				}
			}
			// Output positions, not accidental storage order, determine replay.
			slices.Reverse(assistant.ContinuationItems)
			messages := []llm.Message{{Role: llm.RoleUser, Content: "start"}, assistant}
			for _, call := range assistant.ToolCalls {
				messages = append(messages, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: "ok"})
			}
			before, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			collectNativeAssistant(t, provider, messages)
			request := <-requests
			after, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("replay mutated borrowed messages")
			}
			gotParts := nativeRequestParts(t, request, 1)
			if diff := cmp.Diff(wantParts, gotParts); diff != "" {
				t.Fatalf("replayed parts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNativeContinuationLegacyAndForeign(t *testing.T) {
	t.Parallel()
	provider, requests := nativePartsProvider(t, []string{`[{"text":"ok"}]`})
	call := llm.ToolCall{ID: "call_read_0", Type: "function", OutputIndex: llm.OutputPosition(2), Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"a"}`}}
	messages := []llm.Message{{Role: llm.RoleUser, Content: "start"}, {
		Role: llm.RoleAssistant, Content: "legacy text", ReasoningContent: "not replayed", ToolCalls: []llm.ToolCall{call},
		ContinuationItems: []llm.ContinuationItem{
			{Provider: "google", Format: "generate_content.part.v1", Kind: "function_call", ID: call.ID, OutputIndex: llm.OutputPosition(2), Data: []byte(`{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"c2ln"}`)},
			{Provider: "anthropic", Format: "generate_content.part.v2", Data: []byte(`malformed foreign provider`)},
			{Provider: "google", Format: "foreign.format.v1", Data: []byte(`malformed foreign format`)},
		},
	}}
	collectNativeAssistant(t, provider, messages)
	parts := nativeRequestParts(t, <-requests, 1)
	var want []map[string]any
	decodeNativeTestJSON(t, []byte(`[{"text":"legacy text"},{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"c2ln"}]`), &want)
	if diff := cmp.Diff(want, parts); diff != "" {
		t.Fatalf("legacy replay (-want +got):\n%s", diff)
	}
}

func TestNativeContinuationRejectsMalformedMatchingData(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		format string
		data   string
		index  *int
	}{
		{"invalid JSON", "generate_content.part.v2", `{`, llm.OutputPosition(0)},
		{"null", "generate_content.part.v2", `null`, llm.OutputPosition(0)},
		{"empty", "generate_content.part.v2", `{}`, llm.OutputPosition(0)},
		{"unknown only", "generate_content.part.v2", `{"future":true}`, llm.OutputPosition(0)},
		{"invalid signature", "generate_content.part.v2", `{"thoughtSignature":"!"}`, llm.OutputPosition(0)},
		{"unnamed call", "generate_content.part.v2", `{"functionCall":{"args":{}}}`, llm.OutputPosition(0)},
		{"missing position", "generate_content.part.v2", `{"text":"text"}`, nil},
		{"negative position", "generate_content.part.v2", `{"text":"text"}`, llm.OutputPosition(-1)},
		{"invalid legacy JSON", "generate_content.part.v1", `{`, nil},
		{"unsigned legacy call", "generate_content.part.v1", `{"functionCall":{"name":"read"}}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, []string{`[{"text":"unexpected"}]`})
			var streamErr error
			count := 0
			stream := provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "fallback must not hide corruption", ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: test.format, Data: []byte(test.data), OutputIndex: test.index}}}}})
			for chunk, err := range stream {
				count++
				streamErr = err
				if chunk.Content != "" || len(chunk.CompletedItems) != 0 {
					t.Fatalf("unexpected chunk = %#v", chunk)
				}
			}
			if streamErr == nil || count != 1 {
				t.Fatalf("terminal error = %v, yields = %d", streamErr, count)
			}
			if test.data == `{` {
				var syntaxErr *json.SyntaxError
				if !errors.As(streamErr, &syntaxErr) {
					t.Fatalf("JSON error identity not preserved: %v", streamErr)
				}
			}
			select {
			case request := <-requests:
				t.Fatalf("malformed continuation made an HTTP request: %s", request)
			default:
			}
		})
	}
}

func TestNativeContinuationRejectsConflictingMetadata(t *testing.T) {
	t.Parallel()
	part := llm.ContinuationItem{Provider: "google", Format: "generate_content.part.v2", OutputIndex: llm.OutputPosition(0), ID: "native", Data: []byte(`{"functionCall":{"id":"native","name":"read","args":{"path":"a"}},"thoughtSignature":"c2ln"}`)}
	conflictingID := part.Clone()
	conflictingID.ID = "other"
	legacy := part.Clone()
	legacy.Format = "generate_content.part.v1"
	for _, test := range []struct {
		name  string
		items []llm.ContinuationItem
		calls []llm.ToolCall
	}{
		{name: "duplicate position", items: []llm.ContinuationItem{part, part.Clone()}},
		{name: "conflicting native ID", items: []llm.ContinuationItem{conflictingID}},
		{name: "mixed versions", items: []llm.ContinuationItem{part, legacy}},
		{name: "unrepresented display call", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "other", Function: llm.ToolCallFunction{Name: "write"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, []string{`[{"text":"unexpected"}]`})
			var streamErr error
			yields := 0
			for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: test.calls, ContinuationItems: test.items}}}) {
				yields++
				streamErr = err
			}
			if streamErr == nil || yields != 1 {
				t.Fatalf("terminal error = %v, yields = %d", streamErr, yields)
			}
			select {
			case request := <-requests:
				t.Fatalf("conflicting continuation made an HTTP request: %s", request)
			default:
			}
		})
	}
}

func TestNativeContinuationRejectsIncompleteDisplay(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		content  string
		thinking string
		parts    []string
	}{
		{name: "missing suffix", content: "beforeafter", parts: []string{`{"text":"before"}`}},
		{name: "missing prefix", content: "beforeafter", parts: []string{`{"text":"after"}`}},
		{name: "conflicting content", content: "visible", parts: []string{`{"text":"native-canary"}`}},
		{name: "signature only with content", content: "visible", parts: []string{`{"thoughtSignature":"c2ln"}`}},
		{name: "signature only with reasoning", thinking: "reasoning", parts: []string{`{"thoughtSignature":"c2ln"}`}},
		{name: "missing reasoning suffix", thinking: "beforeafter", parts: []string{`{"text":"before","thought":true}`}},
		{name: "missing reasoning prefix", thinking: "beforeafter", parts: []string{`{"text":"after","thought":true}`}},
		{name: "conflicting thinking", content: "visible", thinking: "display", parts: []string{`{"text":"visible"}`, `{"text":"native-canary","thought":true,"thoughtSignature":"c2ln"}`}},
		{name: "thinking is not visible text", content: "visible", parts: []string{`{"text":"visible","thought":true}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assistant := llm.Message{Role: llm.RoleAssistant, Content: test.content, ReasoningContent: test.thinking}
			for index, data := range test.parts {
				assistant.ContinuationItems = append(assistant.ContinuationItems, nativePartItem(index, data))
			}
			assertNativeRejectedBeforeDispatch(t, assistant)
		})
	}
}

func TestNativeContinuationAcceptsCompleteOrAbsentDisplay(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		content  string
		thinking string
	}{
		{name: "complete", content: "beforeafter", thinking: "private thought"},
		{name: "native only"},
		{name: "reasoning projection absent", content: "beforeafter"},
		{name: "content projection absent", thinking: "private thought"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, []string{`[{"text":"ok"}]`})
			data := []string{`{"text":"private ","thought":true}`, `{"text":"before","thoughtSignature":"YmVmb3Jl"}`, `{"thoughtSignature":"c2ln"}`, `{"text":"thought","thought":true,"thoughtSignature":"dGhvdWdodA=="}`, `{"text":"after"}`}
			assistant := llm.Message{Role: llm.RoleAssistant, Content: test.content, ReasoningContent: test.thinking}
			var want []map[string]any
			for index, part := range data {
				assistant.ContinuationItems = append(assistant.ContinuationItems, nativePartItem(index, part))
				var decoded map[string]any
				decodeNativeTestJSON(t, []byte(part), &decoded)
				want = append(want, decoded)
			}
			slices.Reverse(assistant.ContinuationItems)
			collectNativeAssistant(t, provider, []llm.Message{assistant})
			if diff := cmp.Diff(want, nativeRequestParts(t, <-requests, 0)); diff != "" {
				t.Fatalf("native replay (-want +got):\n%s", diff)
			}
		})
	}
}

func nativePartItem(index int, data string) llm.ContinuationItem {
	return llm.ContinuationItem{Provider: "google", Format: "generate_content.part.v2", OutputIndex: llm.OutputPosition(index), Data: []byte(data)}
}

func assertNativeRejectedBeforeDispatch(t *testing.T, assistant llm.Message) {
	t.Helper()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				select {
				case requests <- struct{}{}:
				default:
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {}\n\n")
			}))
			t.Cleanup(server.Close)
			provider := newGoogleRouteProvider(t, route, server.URL)
			messages := []llm.Message{assistant}
			for _, call := range assistant.ToolCalls {
				messages = append(messages, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: "current result"})
			}
			before, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			yields := 0
			for chunk, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
				yields++
				if streamErr == nil || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatalf("yield = %#v, %v; want empty chunk and terminal error", chunk, streamErr)
				}
				if strings.Contains(streamErr.Error(), "native-canary") || strings.Contains(streamErr.Error(), "c2ln") {
					t.Fatalf("error exposed native data: %v", streamErr)
				}
			}
			if yields != 1 {
				t.Fatalf("terminal yields = %d, want 1", yields)
			}
			select {
			case <-requests:
				t.Fatal("conflicting continuation dispatched an HTTP request")
			default:
			}
			after, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("validation mutated borrowed messages")
			}
		})
	}
}

func nativePartsProvider(t *testing.T, frames []string) (*google.Provider, <-chan []byte) {
	t.Helper()
	return nativePartsRouteProvider(t, googleRoutes[0], frames)
}

func nativePartsRouteProvider(t *testing.T, route googleRoute, frames []string) (*google.Provider, <-chan []byte) {
	t.Helper()
	requests := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		for _, parts := range frames {
			_, _ = fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":%s},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1}}\n\n", parts)
		}
	}))
	t.Cleanup(server.Close)
	return newGoogleRouteProvider(t, route, server.URL), requests
}

func collectNativeAssistant(t *testing.T, provider *google.Provider, messages []llm.Message) llm.Message {
	t.Helper()
	assistant := llm.Message{Role: llm.RoleAssistant}
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: messages}) {
		if err != nil {
			t.Fatal(err)
		}
		owned := chunk.Clone()
		assistant.Content += owned.Content
		assistant.ReasoningContent += owned.Thinking
		assistant.ToolCalls = append(assistant.ToolCalls, owned.ToolCalls...)
		assistant.ContinuationItems = append(assistant.ContinuationItems, owned.CompletedItems...)
	}
	return assistant
}

func nativeRequestParts(t *testing.T, data []byte, index int) []map[string]any {
	t.Helper()
	var request struct {
		Contents []struct {
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	decodeNativeTestJSON(t, data, &request)
	if len(request.Contents) <= index {
		t.Fatalf("missing content %d: %s", index, data)
	}
	return request.Contents[index].Parts
}

// Keep exact numbers in wire assertions: float64 would conceal rounding of
// large integers and precise decimals in signed native arguments or metadata.
func decodeNativeTestJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCaptureRejectsCollidingFunctionIDs(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, test := range []struct {
				name   string
				frames []string
			}{
				{name: "duplicate explicit in one frame", frames: []string{`[{"functionCall":{"id":"native-canary","name":"read","args":{"n":1}}},{"functionCall":{"id":"native-canary","name":"read","args":{"n":2}}}]`}},
				{name: "duplicate explicit across frames", frames: []string{`[{"functionCall":{"id":"native-canary","name":"read","args":{"n":1}}}]`, `[{"functionCall":{"id":"native-canary","name":"write","args":{"n":2}}}]`}},
				{name: "synthetic then explicit", frames: []string{`[{"functionCall":{"name":"read","args":{}}},{"functionCall":{"id":"call_read_0","name":"write","args":{}}}]`}},
				{name: "explicit then synthetic", frames: []string{`[{"functionCall":{"id":"call_read_1","name":"write","args":{}}},{"functionCall":{"name":"read","args":{}}}]`}},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					provider, requests := nativePartsRouteProvider(t, route, test.frames)
					terminal := false
					items := 0
					for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "start"}}}) {
						if terminal {
							t.Fatal("yield after terminal error")
						}
						if err != nil {
							terminal = true
							if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
								t.Fatalf("terminal chunk = %#v", chunk)
							}
							if strings.Contains(err.Error(), "native-canary") || strings.Contains(err.Error(), "call_read_") {
								t.Fatalf("error exposed ID: %v", err)
							}
							continue
						}
						if len(chunk.ToolCalls) != 0 || chunk.UsageReported || chunk.FinishReason != llm.FinishReasons.UNKNOWN {
							t.Fatalf("successful metadata = %#v", chunk)
						}
						items += len(chunk.CompletedItems)
					}
					if !terminal || items != 1 {
						t.Fatalf("terminal = %v, accepted items = %d", terminal, items)
					}
					<-requests
				})
			}
		})
	}
}
