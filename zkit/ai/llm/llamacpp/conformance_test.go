package llamacpp_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

// TestProvider_Conformance plugs the llama.cpp backend into the
// shared providertest harness. llama.cpp speaks the OpenAI chat-
// completion wire format (it's a facade over pkg/ai/llm/openai)
// so the stub handlers are the same shape as the OpenAI ones —
// just routed through the llamacpp NewProvider entry point.
//
// The conformance suite catches the case where llamacpp's facade
// drops something on the floor (e.g. stops honouring ctx.Done,
// stops surfacing tool calls): a real llamacpp server is
// indistinguishable from a real OpenAI server at the wire level,
// so the same scenarios apply.
func TestProvider_Conformance(t *testing.T) {
	factory := func(t *testing.T, baseURL string) llm.Provider {
		p, err := llamacpp.NewProvider(llamacpp.WithBaseURL(baseURL))
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
				Handler:         openaiStyleHangForever(),
				Request:         providertest.SimpleRequest("hi"),
				Assert:          providertest.AssertCancellationHonoured,
				CancelMidStream: true,
				Timeout:         3 * time.Second,
			},
			{
				Name:    "StreamingDone_FinalChunkMarked",
				Handler: openaiStyleSimpleStream(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertStreamingDoneSet,
			},
			{
				Name:    "Usage_ReportedOnFinalChunk",
				Handler: openaiStyleSimpleStreamWithUsage(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertUsageReported,
			},
			{
				Name:    "ToolCalls_SurfacedToCaller",
				Handler: openaiStyleToolCallStream(),
				Request: providertest.RequestWithTool("call the tool"),
				Assert:  providertest.AssertToolCallEmitted("echo"),
			},
		},
	})
}

// The handlers mirror the OpenAI conformance stubs because that's
// what llama.cpp emits on the wire. Duplicated here rather than
// imported from openai's test package so the llamacpp tests stay
// self-contained — a future shape change in the openai stubs
// shouldn't silently invalidate the llamacpp run.

func openaiStyleHangForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}
}

func openaiStyleSimpleStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write(
			[]byte(
				`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"hello"},"index":0}]}` + "\n\n",
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

func openaiStyleSimpleStreamWithUsage() http.HandlerFunc {
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

func openaiStyleToolCallStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
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
