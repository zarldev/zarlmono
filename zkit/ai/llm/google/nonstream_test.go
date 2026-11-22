package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestNonstreamNativeToolLoop(t *testing.T) {
	t.Parallel()
	const parts = `[{"text":"think","thought":true,"thoughtSignature":"dGhvdWdodA=="},{"thoughtSignature":"c2ln"},{"text":"before"},{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"Y2FsbA=="},{"text":"after"},{"functionCall":{"id":"native-write","name":"write","args":{"path":"b"}}}]`
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/models/gemini-2.0-flash:generateContent") || r.URL.RawQuery != "" {
					t.Errorf("nonstream wire = %s %s", r.Method, r.URL)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":`+parts+`},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":7,"totalTokenCount":15,"cachedContentTokenCount":2}}`)
			}))
			t.Cleanup(server.Close)
			provider := newGoogleRouteProvider(t, route, server.URL)
			messages := []llm.Message{{Role: llm.RoleUser, Content: "start"}}
			chunks := collectNonstream(t, provider, messages)
			<-requests
			assistant := llm.Message{Role: llm.RoleAssistant}
			for _, chunk := range chunks {
				assistant.Content += chunk.Content
				assistant.ReasoningContent += chunk.Thinking
				assistant.ToolCalls = append(assistant.ToolCalls, chunk.ToolCalls...)
				assistant.ContinuationItems = append(assistant.ContinuationItems, chunk.CompletedItems...)
			}
			if assistant.Content != "beforeafter" || assistant.ReasoningContent != "think" || len(assistant.ToolCalls) != 2 || len(assistant.ContinuationItems) != 6 {
				t.Fatalf("assistant = %#v", assistant)
			}
			for index, item := range assistant.ContinuationItems {
				if item.Provider != "google" || item.Format != "generate_content.part.v2" || item.OutputIndex == nil || *item.OutputIndex != index {
					t.Fatalf("native item %d = %#v", index, item)
				}
			}
			calls := assistant.ToolCalls
			if calls[0].ID != "call_read_0" || calls[1].ID != "native-write" || *calls[0].OutputIndex != 3 || *calls[1].OutputIndex != 5 {
				t.Fatalf("tool calls = %#v", calls)
			}
			final := chunks[len(chunks)-1]
			wantUsage := llm.Usage{PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15, CachedTokens: 2, ReasoningTokens: 7, ReasoningTokensReported: true}
			if final.FinishReason != llm.FinishReasons.STOP || !final.UsageReported || final.Usage != wantUsage {
				t.Fatalf("metadata = %#v", final)
			}
			slices.Reverse(assistant.ContinuationItems)
			messages = append(messages, assistant)
			for _, call := range calls {
				messages = append(messages, llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: "ok"})
			}
			before, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			collectNonstream(t, provider, messages)
			request := <-requests
			after, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("nonstream replay mutated borrowed history")
			}
			var want []map[string]any
			decodeNativeTestJSON(t, []byte(parts), &want)
			if diff := cmp.Diff(want, nativeRequestParts(t, request, 1)); diff != "" {
				t.Fatalf("native replay (-want +got):\n%s", diff)
			}
			responses := nativeRequestParts(t, request, 2)
			for index, call := range calls {
				response := responses[index]["functionResponse"].(map[string]any)
				if response["id"] != call.ID || response["name"] != call.Function.Name {
					t.Fatalf("function response %d = %#v", index, response)
				}
			}
		})
	}
}

func TestNonstreamExactReplayAndBodyHook(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"generate_content.part.v1", "generate_content.part.v2"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			const part = `{"functionCall":{"id":"native","name":"read","args":{"n":9007199254740993,"fraction":0.10000000000000001}},"thoughtSignature":"c2ln"}`
			var request []byte
			var hooked bool
			transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				var err error
				request, err = io.ReadAll(r.Body)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"STOP"}]}`)), Request: r}, nil
			}}
			client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
				APIKey: "test-key", Backend: genai.BackendGeminiAPI, HTTPClient: &http.Client{Transport: transport},
				HTTPOptions: genai.HTTPOptions{ExtrasRequestProvider: func(body map[string]any) map[string]any {
					hooked = true
					body["hookField"] = "final"
					return body
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			provider, err := google.NewProvider("", google.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			item := nativePartItem(0, part)
			item.Format, item.ID = format, "native"
			collectNonstream(t, provider, []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "native", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":9007199254740993,"fraction":0.10000000000000001}`}}}, ContinuationItems: []llm.ContinuationItem{item}},
				{Role: llm.RoleTool, ToolCallID: "native", Content: "ok"},
			})
			var want map[string]any
			decodeNativeTestJSON(t, []byte(part), &want)
			if diff := cmp.Diff([]map[string]any{want}, nativeRequestParts(t, request, 0)); diff != "" {
				t.Fatalf("exact replay (-want +got):\n%s", diff)
			}
			if !hooked || !strings.Contains(string(request), `"hookField":"final"`) {
				t.Fatalf("client body hook not forwarded: %s", request)
			}
		})
	}
}

func TestNonstreamMetadataHonesty(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, response string
		finish         llm.FinishReason
		usage          bool
	}{
		{name: "absent", response: `{}`, finish: llm.FinishReasons.UNKNOWN},
		{name: "reported zero", response: `{"usageMetadata":{}}`, finish: llm.FinishReasons.UNKNOWN, usage: true},
		{name: "blocked prompt", response: `{"promptFeedback":{"blockReason":"SAFETY"}}`, finish: llm.FinishReasons.UNKNOWN},
		{name: "unknown", response: `{"candidates":[{"finishReason":"FUTURE"}]}`, finish: llm.FinishReasons.UNKNOWN},
		{name: "stop", response: `{"candidates":[{"finishReason":"STOP"}]}`, finish: llm.FinishReasons.STOP},
		{name: "length", response: `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`, finish: llm.FinishReasons.LENGTH},
		{name: "safety", response: `{"candidates":[{"finishReason":"SAFETY"}]}`, finish: llm.FinishReasons.CONTENTFILTER},
		{name: "tool failure", response: `{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`, finish: llm.FinishReasons.TOOLCALLS},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, _ := nonstreamBodyProvider(t, io.NopCloser(strings.NewReader(test.response)))
			chunks := collectNonstream(t, provider, []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
			if test.finish == llm.FinishReasons.UNKNOWN && !test.usage {
				if len(chunks) != 0 {
					t.Fatalf("invented metadata = %#v", chunks)
				}
				return
			}
			if len(chunks) != 1 || chunks[0].FinishReason != test.finish || chunks[0].UsageReported != test.usage || chunks[0].Usage != (llm.Usage{}) {
				t.Fatalf("metadata = %#v", chunks)
			}
		})
	}
}

func TestNonstreamUnsupportedOutputMedia(t *testing.T) {
	t.Parallel()
	for _, media := range []string{`{"inlineData":{"mimeType":"audio/wav","data":"YQ=="}}`, `{"fileData":{"mimeType":"image/png","fileUri":"gs://private-canary"}}`} {
		t.Run(media, func(t *testing.T) {
			t.Parallel()
			body := io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"prefix"},` + media + `]},"finishReason":"STOP"}],"usageMetadata":{}}`))
			provider, _ := nonstreamBodyProvider(t, body)
			yields := 0
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
				yields++
				if yields == 1 && err == nil && chunk.Content == "prefix" {
					continue
				}
				if yields != 2 || !errors.Is(err, llm.ErrModelNotSupported) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatalf("yield %d = %#v, %v", yields, chunk, err)
				}
				if strings.Contains(err.Error(), "private-canary") || strings.Contains(err.Error(), "audio/wav") {
					t.Fatalf("error exposed media: %v", err)
				}
			}
			if yields != 2 {
				t.Fatalf("yields = %d", yields)
			}
		})
	}
}

func TestNonstreamLazyStopAndBodyOwnership(t *testing.T) {
	t.Parallel()
	body := &nonstreamBody{Reader: strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"first"},{"text":"must not emit"},{"functionCall":{"name":"read"}}]},"finishReason":"STOP"}],"usageMetadata":{}}`)}
	provider, requests := nonstreamBodyProvider(t, body)
	stream := provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if *requests != 0 || body.closed {
		t.Fatal("Complete performed HTTP work before iteration")
	}
	yields := 0
	stream(func(chunk llm.CompletionChunk, err error) bool {
		yields++
		if err != nil || chunk.Content != "first" || len(chunk.CompletedItems) != 1 {
			t.Fatalf("first yield = %#v, %v", chunk, err)
		}
		return false
	})
	if yields != 1 || *requests != 1 || !body.closed {
		t.Fatalf("yields=%d requests=%d body closed=%v", yields, *requests, body.closed)
	}
}

func TestNonstreamSDKDecodeErrorAndBodyOwnership(t *testing.T) {
	t.Parallel()
	body := &nonstreamBody{Reader: strings.NewReader(`{"candidates":!}`)}
	provider, requests := nonstreamBodyProvider(t, body)
	yields := 0
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
		yields++
		var syntax *json.SyntaxError
		if !errors.As(err, &syntax) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
			t.Fatalf("decode failure = %#v, %v", chunk, err)
		}
	}
	if yields != 1 || *requests != 1 || !body.closed {
		t.Fatalf("yields=%d requests=%d body closed=%v", yields, *requests, body.closed)
	}
}

func TestNonstreamCancellationCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("nonstream test cancellation")
	for _, beforeIteration := range []bool{true, false} {
		t.Run(map[bool]string{true: "before iteration", false: "during read"}[beforeIteration], func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			body := &nonstreamBody{Reader: strings.NewReader(`{}`)}
			if beforeIteration {
				cancel(cause)
			} else {
				body.onRead = func() { cancel(cause) }
			}
			provider, requests := nonstreamBodyProvider(t, body)
			yields := 0
			for chunk, err := range provider.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}) {
				yields++
				if !errors.Is(err, cause) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatalf("cancellation = %#v, %v", chunk, err)
				}
			}
			if yields != 1 || beforeIteration && *requests != 0 || !beforeIteration && (*requests != 1 || !body.closed) {
				t.Fatalf("yields=%d requests=%d body closed=%v", yields, *requests, body.closed)
			}
		})
	}
}

func collectNonstream(t *testing.T, provider *google.Provider, messages []llm.Message) []llm.CompletionChunk {
	t.Helper()
	var chunks []llm.CompletionChunk
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, chunk.Clone())
	}
	return chunks
}

func nonstreamBodyProvider(t *testing.T, body io.ReadCloser) (*google.Provider, *int) {
	t.Helper()
	requests := 0
	transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		requests++
		if !strings.HasSuffix(r.URL.Path, ":generateContent") || r.URL.RawQuery != "" {
			t.Errorf("nonstream wire = %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: body, Request: r}, nil
	}}
	provider, err := google.NewProvider("test-key", google.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	return provider, &requests
}

type nonstreamBody struct {
	io.Reader
	onRead func()
	closed bool
}

func (b *nonstreamBody) Read(p []byte) (int, error) {
	if b.onRead != nil {
		b.onRead()
	}
	return b.Reader.Read(p)
}

func (b *nonstreamBody) Close() error {
	b.closed = true
	return nil
}
