package anthropic_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

// TestProvider_Conformance plugs the Anthropic backend into the
// shared providertest harness. Anthropic's streaming wire format is
// a sequence of typed events (message_start, content_block_start,
// content_block_delta, …) instead of a stream of choices[] chunks —
// the assertions on top are the same, only the stub bytes differ.
func TestProvider_Conformance(t *testing.T) {
	factory := func(t *testing.T, baseURL string) llm.Provider {
		p, err := anthropic.NewProvider("test-key", anthropic.WithBaseURL(baseURL))
		if err != nil {
			t.Fatalf("NewProvider: %v", err)
		}
		return p
	}

	providertest.Run(t, providertest.Suite{
		Factory: factory,
		Scenarios: []providertest.Scenario{
			{
				Name:            "Cancellation_HandlerHangsForever",
				Handler:         anthropicHangForever(),
				Request:         providertest.SimpleRequest("hi"),
				Assert:          providertest.AssertCancellationHonoured,
				CancelMidStream: true,
				Timeout:         3 * time.Second,
			},
			{
				Name:    "StreamingDone_FinalChunkMarked",
				Handler: anthropicSimpleStream(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertStreamingDoneSet,
			},
			{
				Name:    "Usage_ReportedOnFinalChunk",
				Handler: anthropicSimpleStreamWithUsage(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertUsageReported,
			},
			{
				Name:    "ToolCalls_SurfacedToCaller",
				Handler: anthropicToolCallStream(),
				Request: providertest.RequestWithTool("call the tool"),
				Assert:  providertest.AssertToolCallEmitted("echo"),
			},
		},
	})
}

// anthropicHangForever holds the SSE connection open until the
// client cancels. The Anthropic SDK should propagate ctx.Done
// through to the underlying stream and tear down cleanly.
func anthropicHangForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}
}

// anthropicSimpleStream emits the minimum event sequence for a
// text-only response: message_start → content_block_start (text) →
// content_block_delta → content_block_stop → message_delta →
// message_stop. Used for streaming-done validation.
func anthropicSimpleStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		writeAnthropicEvent(
			w,
			"message_start",
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":4,"output_tokens":0}}}`,
		)
		writeAnthropicEvent(
			w,
			"content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		)
		writeAnthropicEvent(
			w,
			"content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		)
		writeAnthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeAnthropicEvent(
			w,
			"message_delta",
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
		)
		writeAnthropicEvent(w, "message_stop", `{"type":"message_stop"}`)
		if f != nil {
			f.Flush()
		}
	}
}

// anthropicSimpleStreamWithUsage adds non-zero usage to both
// message_start (input_tokens) and message_delta (output_tokens) so
// the provider's final-chunk Usage{} is populated. The provider
// reads usage off the accumulated message, not off individual
// events — so both halves matter.
func anthropicSimpleStreamWithUsage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		writeAnthropicEvent(
			w,
			"message_start",
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":7,"output_tokens":0}}}`,
		)
		writeAnthropicEvent(
			w,
			"content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		)
		writeAnthropicEvent(
			w,
			"content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		)
		writeAnthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeAnthropicEvent(
			w,
			"message_delta",
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}`,
		)
		writeAnthropicEvent(w, "message_stop", `{"type":"message_stop"}`)
		if f != nil {
			f.Flush()
		}
	}
}

// anthropicToolCallStream emits a content_block of type tool_use
// with the function name + a single input_json_delta that finalises
// the arguments JSON. The provider surfaces the complete tool call
// on content_block_stop.
func anthropicToolCallStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		writeAnthropicEvent(
			w,
			"message_start",
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}`,
		)
		writeAnthropicEvent(
			w,
			"content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"echo","input":{}}}`,
		)
		writeAnthropicEvent(
			w,
			"content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\"hi\"}"}}`,
		)
		writeAnthropicEvent(w, "content_block_stop", `{"type":"content_block_stop","index":0}`)
		writeAnthropicEvent(
			w,
			"message_delta",
			`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":4}}`,
		)
		writeAnthropicEvent(w, "message_stop", `{"type":"message_stop"}`)
		if f != nil {
			f.Flush()
		}
	}
}

// writeAnthropicEvent formats one SSE event with both the `event:`
// type line and the `data:` payload. The Anthropic SDK requires the
// event: field to dispatch typed events; without it the SDK treats
// every chunk as the default type and the test wouldn't exercise
// the real decode path.
func writeAnthropicEvent(w http.ResponseWriter, eventType, data string) {
	_, _ = w.Write([]byte("event: " + eventType + "\n"))
	_, _ = w.Write([]byte("data: " + data + "\n\n"))
}
