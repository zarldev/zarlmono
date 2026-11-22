package openai_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

type translatedChatTransport struct {
	calls  atomic.Int64
	closed atomic.Bool
}

func (t *translatedChatTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &translatedChatBody{Reader: strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"thinking_blocks\":[{\"type\":\"thinking\",\"thinking\":\"private\",\"index\":0}]}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"thinking_blocks\":[{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"signed\",\"index\":0}]},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), closed: &t.closed}, Request: req}, nil
}

type translatedChatBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b *translatedChatBody) Close() error { b.closed.Store(true); return nil }

func TestLiteLLMChatExplicitExtensionLifecycle(t *testing.T) {
	t.Run("lazy validation", func(t *testing.T) {
		transport := &translatedChatTransport{}
		p := openai.NewProvider("key", openai.WithBaseURL("http://gateway.invalid/v1"), openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
		stream := p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: "unknown"}}})
		if transport.calls.Load() != 0 {
			t.Fatal("Complete was eager")
		}
		terminal := 0
		for _, err := range stream {
			if !errors.Is(err, openai.ErrLiteLLMChat) {
				t.Fatalf("error=%v", err)
			}
			terminal++
		}
		if terminal != 1 || transport.calls.Load() != 0 {
			t.Fatalf("terminal=%d calls=%d", terminal, transport.calls.Load())
		}
	})
	t.Run("early stop owns close without publishing partial", func(t *testing.T) {
		transport := &translatedChatTransport{}
		p := openai.NewProvider("key", openai.WithBaseURL("http://gateway.invalid/v1"), openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
		stream := p.Complete(t.Context(), llm.CompletionRequest{Stream: true})
		if transport.calls.Load() != 0 {
			t.Fatal("Complete was eager")
		}
		count := 0
		stream(func(chunk llm.CompletionChunk, err error) bool {
			count++
			if err != nil || chunk.Thinking != "private" || len(chunk.CompletedItems) != 0 {
				t.Fatalf("chunk=%+v err=%v", chunk, err)
			}
			return false
		})
		if count != 1 || transport.calls.Load() != 1 || !transport.closed.Load() {
			t.Fatalf("yields=%d calls=%d closed=%v", count, transport.calls.Load(), transport.closed.Load())
		}
	})
	t.Run("cancellation cause before validation", func(t *testing.T) {
		transport := &translatedChatTransport{}
		p := openai.NewProvider("key", openai.WithBaseURL("http://gateway.invalid/v1"), openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("host cancelled")
		cancel(cause)
		for _, err := range p.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: "unknown"}}}) {
			if !errors.Is(err, cause) {
				t.Fatalf("error=%v", err)
			}
		}
		if transport.calls.Load() != 0 {
			t.Fatal("cancelled request dispatched")
		}
	})
}

type translatedResponseTransport struct {
	body   string
	stream bool
	calls  atomic.Int64
}

func (t *translatedResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	contentType := "application/json"
	if t.stream {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(t.body)), Request: req}, nil
}

func translatedEvent(delta, finish string) string {
	return `data: {"choices":[{"index":0,"delta":` + delta + `,"finish_reason":"` + finish + `"}]}` + "\n\n"
}

func translatedResponse(message string) string {
	return `{"choices":[{"index":0,"message":` + message + `,"finish_reason":"stop"}],"usage":{}}`
}

func TestLiteLLMChatBoundedExternalAndAggregateState(t *testing.T) {
	const oneMiB = 1 << 20
	const eightMiB = 8 << 20
	var blocks, tooManyBlocks, calls, tooManyCalls, streamBlocks, streamCalls strings.Builder
	for i := range 257 {
		if i < 129 {
			streamBlocks.WriteString(translatedEvent(fmt.Sprintf(`{"thinking_blocks":[{"type":"redacted_thinking","data":"opaque","index":%d}]}`, i), ""))
		}
		streamCalls.WriteString(translatedEvent(fmt.Sprintf(`{"tool_calls":[{"index":%d,"id":"call%d","type":"function","function":{"name":"echo","arguments":"{}"}}]}`, i, i), ""))
		if i < 129 {
			if i != 0 {
				tooManyBlocks.WriteByte(',')
			}
			tooManyBlocks.WriteString(`{"type":"redacted_thinking","data":"opaque"}`)
			if i == 127 {
				blocks.WriteString(tooManyBlocks.String())
			}
		}
		if i != 0 {
			tooManyCalls.WriteByte(',')
		}
		fmt.Fprintf(&tooManyCalls, `{"id":"call%d","type":"function","function":{"name":"echo","arguments":"{}"}}`, i)
		if i == 255 {
			calls.WriteString(tooManyCalls.String())
		}
	}
	fragment := translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"`+strings.Repeat("t", 1024)+`","index":0}]}`, "")
	argsFirst := translatedEvent(`{"tool_calls":[{"index":0,"id":"real","type":"function","function":{"name":"echo","arguments":"{"}}]}`, "")
	argsFragment := translatedEvent(`{"tool_calls":[{"index":0,"function":{"arguments":"`+strings.Repeat(" ", 1024)+`"}}]}`, "")
	signed := translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private-bound-canary","signature":"s","index":0}]}`, "stop")
	cases := []struct {
		name, body string
		stream     bool
		want       error
	}{
		{name: "block below byte bound", body: translatedResponse(`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"` + strings.Repeat("t", oneMiB-256) + `","signature":"s"}]}`)},
		{name: "block byte bound", body: translatedResponse(`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"` + strings.Repeat("t", oneMiB+1) + `","signature":"s"}]}`), want: openai.ErrLiteLLMChat},
		{name: "envelope bytes", body: translatedResponse(`{"role":"assistant","content":"` + strings.Repeat("x", eightMiB+1) + `"}`), want: openai.ErrLiteLLMChat},
		{name: "128 blocks", body: translatedResponse(`{"role":"assistant","thinking_blocks":[` + blocks.String() + `]}`)},
		{name: "129 blocks", body: translatedResponse(`{"role":"assistant","thinking_blocks":[` + tooManyBlocks.String() + `]}`), want: openai.ErrLiteLLMChat},
		{name: "256 calls", body: translatedResponse(`{"role":"assistant","tool_calls":[` + calls.String() + `]}`)},
		{name: "257 calls", body: translatedResponse(`{"role":"assistant","tool_calls":[` + tooManyCalls.String() + `]}`), want: openai.ErrLiteLLMChat},
		{name: "129 blocks across events", body: streamBlocks.String(), stream: true, want: openai.ErrLiteLLMChat},
		{name: "257 calls across events", body: streamCalls.String(), stream: true, want: openai.ErrLiteLLMChat},
		{name: "many thinking fragments exceed block", body: strings.Repeat(fragment, 1025), stream: true, want: openai.ErrLiteLLMChat},
		{name: "many argument fragments exceed call", body: argsFirst + strings.Repeat(argsFragment, 1025), stream: true, want: openai.ErrLiteLLMChat},
		{name: "many small events exceed fragment count", body: strings.Repeat("data: {\"choices\":[]}\n\n", 65537), stream: true, want: openai.ErrLiteLLMChat},
		{name: "aggregate across events", body: strings.Repeat(translatedEvent(`{"content":"`+strings.Repeat("x", 4096)+`"}`, ""), 2048), stream: true, want: openai.ErrLiteLLMChat},
		{name: "aggregate after finish publishes no envelope", body: signed + strings.Repeat("data: {\"choices\":[],\"usage\":{\"uninterpreted\":\""+strings.Repeat("x", 4096)+"\"}}\n\n", 2048), stream: true, want: openai.ErrLiteLLMChat},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			transport := &translatedResponseTransport{body: tt.body, stream: tt.stream}
			p := openai.NewProvider("bound-key-canary", openai.WithBaseURL("http://gateway.invalid/v1"), openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
			terminal, completed := 0, 0
			var got error
			for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: tt.stream}) {
				completed += len(chunk.CompletedItems)
				if err != nil {
					terminal++
					got = err
				}
			}
			if !errors.Is(got, tt.want) {
				t.Fatalf("error=%v want=%v", got, tt.want)
			}
			if tt.want != nil && (terminal != 1 || completed != 0) {
				t.Fatalf("terminal=%d completed=%d", terminal, completed)
			}
			if got != nil && (strings.Contains(got.Error(), "bound-key-canary") || strings.Contains(got.Error(), "private-bound-canary")) {
				t.Fatal("private diagnostic")
			}
		})
	}
}

func TestLiteLLMChatReplayBoundsFailBeforeHTTP(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  llm.CompletionRequest
	}{
		{name: "oversize stored envelope", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "litellm", Format: "translated_chat.v1", Kind: "assistant", Data: []byte(strings.Repeat("x", (8<<20)+1))}}}}}},
		{name: "aggregate message bytes", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("x", 4<<20)}, {Role: llm.RoleUser, Content: strings.Repeat("x", (4<<20)+1)}}}},
		{name: "oversize replay arguments", req: llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "real", Type: "function", Function: llm.ToolCallFunction{Name: "echo", Arguments: strings.Repeat("x", (1<<20)+1)}}}}}}},
		{name: "message count", req: llm.CompletionRequest{Messages: make([]llm.Message, 1025)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := &translatedResponseTransport{}
			p := openai.NewProvider("key", openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
			terminal := 0
			for chunk, err := range p.Complete(t.Context(), tt.req) {
				if !errors.Is(err, openai.ErrLiteLLMChat) || len(chunk.CompletedItems) != 0 {
					t.Fatalf("error=%v", err)
				}
				terminal++
			}
			if terminal != 1 || transport.calls.Load() != 0 {
				t.Fatal("oversized replay dispatched")
			}
		})
	}
}

func TestLiteLLMChatCancelDuringFragmentAccumulation(t *testing.T) {
	transport := &translatedResponseTransport{stream: true, body: translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"partial","index":0}]}`, "") + translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"s","index":0}]}`, "stop")}
	p := openai.NewProvider("key", openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("host stopped accumulation")
	terminal := 0
	for chunk, err := range p.Complete(ctx, llm.CompletionRequest{Stream: true}) {
		if len(chunk.CompletedItems) != 0 {
			t.Fatal("cancelled state published")
		}
		if err != nil {
			if !errors.Is(err, cause) {
				t.Fatalf("error=%v", err)
			}
			terminal++
		} else {
			cancel(cause)
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal=%d", terminal)
	}
}

func TestLiteLLMControlledScopeOwnerBindsRouteModelAndProtocol(t *testing.T) {
	owner := &openai.LiteLLMReplayScope{} // Construction does not acquire randomness.
	transport := &translatedResponseTransport{body: translatedResponse(`{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}]}`)}
	build := func(key, route, model string, responses bool) *openai.Provider {
		return openai.NewProvider(key, openai.WithBaseURL(route), openai.WithModel(model), openai.WithResponsesAPI(responses), openai.WithLiteLLMChat(), openai.WithLiteLLMReplayScope(owner), openai.WithHTTPClient(&http.Client{Transport: transport}))
	}
	first := build("key", "http://gateway.invalid/v1", "model", false)
	var message llm.Message
	message.Role = llm.RoleAssistant
	stream := first.Complete(t.Context(), llm.CompletionRequest{})
	if transport.calls.Load() != 0 {
		t.Fatal("eager constructor or Complete")
	}
	for chunk, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range chunk.CompletedItems {
			message.ContinuationItems = append(message.ContinuationItems, item.Clone())
		}
	}
	// Only per-call codecs of the same concrete gateway share an owner. Both
	// codecs independently bind nonsecret route/model/protocol to that owner.
	same := build("key", "http://gateway.invalid/v1", "model", false)
	for _, err := range same.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{message}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	before := transport.calls.Load()
	for _, tt := range []struct {
		name, route, model string
		key                string
		responses          bool
	}{
		{name: "credential", key: "other-key", route: "http://gateway.invalid/v1", model: "model"},
		{name: "route", route: "http://other.invalid/v1", model: "model"},
		{name: "model", route: "http://gateway.invalid/v1", model: "other"},
		{name: "protocol", route: "http://gateway.invalid/v1", model: "model", responses: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.key
			if key == "" {
				key = "key"
			}
			p := build(key, tt.route, tt.model, tt.responses)
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{message}}) {
				if !errors.Is(err, openai.ErrLiteLLMChat) {
					t.Fatalf("error=%v", err)
				}
			}
			if transport.calls.Load() != before {
				t.Fatal("mismatched replay dispatched")
			}
		})
	}
	// Direct explicit extensions use fresh owners without a host identity override.
	direct := openai.NewProvider("key", openai.WithBaseURL("http://gateway.invalid/v1"), openai.WithModel("model"), openai.WithLiteLLMChat(), openai.WithHTTPClient(&http.Client{Transport: transport}))
	for _, err := range direct.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{message}}) {
		if !errors.Is(err, openai.ErrLiteLLMChat) {
			t.Fatalf("error=%v", err)
		}
	}
	if transport.calls.Load() != before {
		t.Fatal("new direct provider resumed old scope")
	}
}

func TestLiteLLMChatAggregateReplayCountsAndBytes(t *testing.T) {
	transport := &translatedResponseTransport{body: translatedResponse(`{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}]}`)}
	p := openai.NewProvider("key", openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
	first := llm.Message{Role: llm.RoleAssistant}
	for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range chunk.CompletedItems {
			first.ContinuationItems = append(first.ContinuationItems, item.Clone())
		}
	}
	for _, tt := range []struct {
		name     string
		messages []llm.Message
		want     error
	}{
		{name: "1024 messages", messages: repeatChatMessage(llm.Message{Role: llm.RoleUser}, 1024)},
		{name: "1025 messages", messages: repeatChatMessage(llm.Message{Role: llm.RoleUser}, 1025), want: openai.ErrLiteLLMChat},
		{name: "128 blocks across envelopes", messages: repeatChatMessage(first, 128)},
		{name: "129 blocks across envelopes", messages: repeatChatMessage(first, 129), want: openai.ErrLiteLLMChat},
		{name: "257 continuation items", messages: []llm.Message{{Role: llm.RoleUser, ContinuationItems: make([]llm.ContinuationItem, 257)}}, want: openai.ErrLiteLLMChat},
		{name: "aggregate part bytes", messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.TextPart(strings.Repeat("x", 4<<20)), llm.TextPart(strings.Repeat("x", (4<<20)+1))}}}, want: openai.ErrLiteLLMChat},
		{name: "part count", messages: []llm.Message{{Role: llm.RoleUser, Parts: make([]llm.ContentPart, 257)}}, want: openai.ErrLiteLLMChat},
		{name: "aggregate opaque metadata", messages: []llm.Message{{Role: llm.RoleUser, ContinuationItems: []llm.ContinuationItem{{Provider: strings.Repeat("x", (8<<20)+1)}}}}, want: openai.ErrLiteLLMChat},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := transport.calls.Load()
			terminal := 0
			var got error
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: tt.messages}) {
				if err != nil {
					got = err
					terminal++
				}
			}
			if !errors.Is(got, tt.want) {
				t.Fatalf("error=%v want=%v", got, tt.want)
			}
			if tt.want != nil && (terminal != 1 || transport.calls.Load() != before) {
				t.Fatal("oversized history dispatched")
			}
			if tt.want == nil && transport.calls.Load() != before+1 {
				t.Fatal("bounded valid history rejected")
			}
		})
	}
	for _, n := range []int{256, 257} {
		t.Run(fmt.Sprintf("%d calls across messages", n), func(t *testing.T) {
			var messages []llm.Message
			for i := range n {
				id := fmt.Sprintf("real%d", i)
				messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: "echo", Arguments: "{}"}}}}, llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "result"})
			}
			before := transport.calls.Load()
			var got error
			for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
				if err != nil {
					got = err
				}
			}
			if n == 256 {
				if got != nil || transport.calls.Load() != before+1 {
					t.Fatalf("valid count rejected: %v", got)
				}
			} else if !errors.Is(got, openai.ErrLiteLLMChat) || transport.calls.Load() != before {
				t.Fatalf("oversized count dispatched: %v", got)
			}
		})
	}
}

func repeatChatMessage(message llm.Message, count int) []llm.Message {
	messages := make([]llm.Message, count)
	for i := range messages {
		messages[i] = message
	} // Deliberately shared borrowed state.
	return messages
}

func TestLiteLLMChatFragmentedStateBelowLimitsCompletes(t *testing.T) {
	first := translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","index":0}]}`, "")
	fragment := translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"`+strings.Repeat("t", 1024)+`","index":0}]}`, "")
	signature := translatedEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"","signature":"s","index":0}]}`, "stop")
	for _, tt := range []struct {
		name, body   string
		wantItems    int
		wantThinking int
	}{
		{name: "many small fragments", body: first + strings.Repeat(fragment, 1000) + signature, wantItems: 1, wantThinking: 1000 * 1024},
		{name: "65536 events", body: strings.Repeat("data: {\"choices\":[]}\n\n", 65535) + translatedEvent(`{}`, "stop")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := &translatedResponseTransport{stream: true, body: tt.body}
			p := openai.NewProvider("key", openai.WithHTTPClient(&http.Client{Transport: transport}), openai.WithLiteLLMChat())
			completed, thinking := 0, 0
			for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true}) {
				if err != nil {
					t.Fatal(err)
				}
				completed += len(chunk.CompletedItems)
				thinking += len(chunk.Thinking)
			}
			if completed != tt.wantItems || thinking != tt.wantThinking {
				t.Fatalf("completed=%d thinking-bytes=%d", completed, thinking)
			}
		})
	}
}
