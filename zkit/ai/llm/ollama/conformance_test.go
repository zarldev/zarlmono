package ollama_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/ollama"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

// TestProvider_Conformance plugs the Ollama backend into the
// shared providertest harness. Ollama exposes an OpenAI-compatible
// /v1/* surface (the provider in this package is a thin facade
// over pkg/ai/llm/openai) so the stub handlers are the same shape
// as the OpenAI conformance ones.
//
// As with llamacpp, the conformance run catches a facade dropping
// behaviour: ctx propagation, tool-call surfacing, usage reporting,
// streaming Done. A real Ollama server speaking /v1/chat/completions
// is wire-indistinguishable from OpenAI, so the same scenarios apply.
func TestProvider_Conformance(t *testing.T) {
	factory := func(t *testing.T, baseURL string) llm.Provider {
		p, err := ollama.NewProvider(ollama.WithBaseURL(baseURL))
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
				Handler:         ollamaHangForever(),
				Request:         providertest.SimpleRequest("hi"),
				Assert:          providertest.AssertCancellationHonoured,
				CancelMidStream: true,
				Timeout:         3 * time.Second,
			},
			{
				Name:    "StreamingDone_FinalChunkMarked",
				Handler: ollamaSimpleStream(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertStreamingDoneSet,
			},
			{
				Name:    "Usage_ReportedOnFinalChunk",
				Handler: ollamaSimpleStreamWithUsage(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertUsageReported,
			},
			{
				Name:    "ToolCalls_SurfacedToCaller",
				Handler: ollamaToolCallStream(),
				Request: providertest.RequestWithTool("call the tool"),
				Assert:  providertest.AssertToolCallEmitted("echo"),
			},
		},
	})
}

// The stub handlers mirror the OpenAI conformance shape because
// Ollama emits the same wire format on its /v1/ surface.

func ollamaHangForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}
}

func ollamaSimpleStream() http.HandlerFunc {
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

func ollamaSimpleStreamWithUsage() http.HandlerFunc {
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

func ollamaToolCallStream() http.HandlerFunc {
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
