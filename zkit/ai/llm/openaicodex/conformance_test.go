package openaicodex_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

// TestProvider_Conformance plugs the OpenAICodex backend into the
// shared providertest harness. Codex speaks the Responses API
// (text deltas via response.output_text.delta events) so the wire
// stubs look very different from openai's chat.completion.chunk
// stream — but the assertions on top are the same.
//
// The Factory wires a vault-free StaticTokenSource so the test
// doesn't need real OAuth machinery.
func TestProvider_Conformance(t *testing.T) {
	factory := func(t *testing.T, baseURL string) llm.Provider {
		p, err := openaicodex.NewProvider(
			openaicodex.StaticTokenSource{T: freshToken(t, "acct_conformance")},
			openaicodex.WithBaseURL(baseURL),
		)
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
				Handler:         codexHangForever(),
				Request:         providertest.SimpleRequest("hi"),
				Assert:          providertest.AssertCancellationHonoured,
				CancelMidStream: true,
				Timeout:         3 * time.Second,
			},
			{
				Name:    "StreamingDone_FinalChunkMarked",
				Handler: codexSimpleStream(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertStreamingDoneSet,
			},
			{
				Name:    "Usage_ReportedOnFinalChunk",
				Handler: codexSimpleStreamWithUsage(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertUsageReported,
			},
			{
				Name:    "ToolCalls_SurfacedToCaller",
				Handler: codexToolCallStream(),
				Request: providertest.RequestWithTool("call the tool"),
				Assert:  providertest.AssertToolCallEmitted("echo"),
			},
		},
	})
}

// codexHangForever returns the SSE headers then blocks on the
// request ctx so the provider has to honour client cancellation.
// All conformance stubs route to /codex/responses since that's the
// path the codex client POSTs to.
func codexHangForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			http.Error(w, "wrong path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}
}

// codexSimpleStream emits two text deltas + a response.completed
// event with empty usage. Used for streaming/done validation.
func codexSimpleStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"Hello"}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":", world"}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"usage":{}}}` + "\n\n"))
		if f != nil {
			f.Flush()
		}
	}
}

// codexSimpleStreamWithUsage adds a populated usage block to the
// terminal response.completed event so AssertUsageReported has
// something non-zero to find.
func codexSimpleStreamWithUsage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n"))
		_, _ = w.Write(
			[]byte(
				`data: {"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}` + "\n\n",
			),
		)
		if f != nil {
			f.Flush()
		}
	}
}

// codexToolCallStream returns a function_call_arguments.done event
// — the Responses-API shape for a finished function call. Codex's
// stream emits function_call events with the function name + a
// finalised arguments string; the provider translates these into
// CompletionChunk.ToolCalls.
func codexToolCallStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		// response.output_item.added introduces the function_call;
		// function_call_arguments.done finalises arguments.
		_, _ = w.Write(
			[]byte(
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"echo","arguments":""}}` + "\n\n",
			),
		)
		_, _ = w.Write(
			[]byte(
				`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"text\":\"hi\"}"}` + "\n\n",
			),
		)
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"usage":{}}}` + "\n\n"))
		if f != nil {
			f.Flush()
		}
	}
}
