package openai_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

// TestProvider_Conformance plugs the OpenAI backend into the shared
// providertest harness. Each scenario stubs the chat/completions
// SSE endpoint with the bytes appropriate for its assertion; the
// providertest helpers do the chunk collection + contract checks.
//
// New scenarios for the OpenAI backend get added HERE (the wire
// stub stays close to the provider's actual SSE format), not in
// providertest itself.
func TestProvider_Conformance(t *testing.T) {
	factory := func(t *testing.T, baseURL string) llm.Provider {
		p, err := openai.NewProvider("test-key", openai.WithBaseURL(baseURL))
		if err != nil {
			t.Fatalf("NewProvider: %v", err)
		}
		return p
	}

	providertest.Run(t, providertest.Suite{
		Factory: factory,
		Scenarios: []providertest.Scenario{
			{
				Name:    "Cancellation_HandlerHangsForever",
				Handler: openaiHangForever(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertCancellationHonoured,
				// CancelMidStream isn't strictly needed — the
				// scenario's context timeout (defaultScenarioTimeout)
				// will fire — but kicking cancel manually after
				// the connection establishes is faster and more
				// deterministic.
				CancelMidStream: true,
				Timeout:         3 * time.Second,
			},
			{
				Name:    "StreamingDone_FinalChunkMarked",
				Handler: openaiSimpleStream(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertStreamingDoneSet,
			},
			{
				Name:    "Usage_ReportedOnFinalChunk",
				Handler: openaiSimpleStreamWithUsage(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertUsageReported,
			},
			{
				Name:    "ToolCalls_SurfacedToCaller",
				Handler: openaiToolCallStream(),
				Request: providertest.RequestWithTool("call the tool"),
				Assert:  providertest.AssertToolCallEmitted("echo"),
			},
			{
				Name:    "StreamingError_TerminalChunk",
				Handler: openaiStreamingError(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertErrorSurfaced,
			},
		},
	})
}

// openaiHangForever returns a handler that accepts the request,
// writes the SSE headers, then never sends a chunk. The Provider's
// stream loop is supposed to honour ctx.Done; the scenario's
// CancelMidStream:true triggers cancel ~100ms in.
func openaiHangForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block on the request ctx. When the client cancels, the
		// underlying connection closes and the server-side handler
		// returns naturally.
		<-r.Context().Done()
	}
}

// openaiSimpleStream returns a handler that emits two short content
// deltas + a stop. Used for streaming/done validation.
func openaiSimpleStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		// Two delta chunks + stop. The OpenAI SDK requires the
		// outer choices[] envelope shape.
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"hello"},"index":0}]}` + "\n\n",
			),
		)
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":" world"},"index":0}]}` + "\n\n",
			),
		)
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop","index":0}]}` + "\n\n",
			),
		)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f != nil {
			f.Flush()
		}
	}
}

// openaiSimpleStreamWithUsage extends openaiSimpleStream with a
// trailing usage chunk — the wire shape OpenAI emits when
// stream_options.include_usage=true. The provider sets that flag
// unconditionally; the test backend has to honour it.
func openaiSimpleStreamWithUsage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"ok"},"index":0}]}` + "\n\n",
			),
		)
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop","index":0}]}` + "\n\n",
			),
		)
		// Trailing usage chunk with empty choices[] — the SDK's
		// stream loop accumulates this into the final chunk's Usage.
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}` + "\n\n",
			),
		)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f != nil {
			f.Flush()
		}
	}
}

// openaiToolCallStream emits a single tool-call chunk for the
// "echo" function the conformance scenario advertises. The chunk
// shape mirrors what the real OpenAI API does for assistant
// tool_calls.
func openaiToolCallStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		// tool_calls arrive as an array under delta. Each tool-call
		// has its own index; the first delta carries the function
		// name + id, subsequent deltas (which we don't bother with
		// here for brevity) would carry argument fragments.
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"index":0}]}` + "\n\n",
			),
		)
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"tool_calls","index":0}]}` + "\n\n",
			),
		)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f != nil {
			f.Flush()
		}
	}
}

// openaiStreamingError emits malformed SSE so the SDK surfaces a stream error
// after the provider's read loop. The provider must translate that into a
// terminal error chunk rather than an error-only chunk followed by close.
func openaiStreamingError() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {not-json}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}
