package google_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

// TestProvider_Conformance plugs the Google (Gemini) backend into
// the shared providertest harness. Gemini's streaming wire format
// is SSE where each event's data is a GenerateContentResponse JSON
// — different from OpenAI's chat.completion.chunk and Anthropic's
// typed events, but the assertions on top are identical.
//
// genai SDK uses an "AIza..."-shaped API key. Any non-empty key
// passes provider construction; the SDK delegates auth to the
// test server which doesn't check.
func TestProvider_Conformance(t *testing.T) {
	factory := func(t *testing.T, baseURL string) llm.Provider {
		p, err := google.NewProvider("AIzaTestKey",
			google.WithBaseURL(baseURL),
			// Pick a model that exists in the SDK's accepted list;
			// the test server ignores it but the SDK validates the
			// URL it constructs.
			google.WithModel("gemini-2.0-flash"),
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
				Handler:         googleHangForever(),
				Request:         providertest.SimpleRequest("hi"),
				Assert:          providertest.AssertCancellationHonoured,
				CancelMidStream: true,
				Timeout:         3 * time.Second,
			},
			{
				Name:    "StreamingDone_FinalChunkMarked",
				Handler: googleSimpleStream(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertStreamingDoneSet,
			},
			{
				Name:    "Usage_ReportedOnFinalChunk",
				Handler: googleSimpleStreamWithUsage(),
				Request: providertest.SimpleRequest("hi"),
				Assert:  providertest.AssertUsageReported,
			},
			{
				Name:    "ToolCalls_SurfacedToCaller",
				Handler: googleToolCallStream(),
				Request: providertest.RequestWithTool("call the tool"),
				Assert:  providertest.AssertToolCallEmitted("echo"),
			},
		},
	})
}

// googleHangForever holds the SSE connection open until the client
// cancels. Gemini's SDK streams through GenerateContentStream;
// closing the underlying connection should drive the SDK iterator
// to return ctx.Err().
func googleHangForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}
}

// googleSimpleStream emits a Gemini-shape GenerateContentResponse
// with a short text candidate + finishReason STOP. The SDK turns
// this into a text chunk + a usage chunk.
func googleSimpleStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write(
			[]byte(
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}` + "\n\n",
			),
		)
		if f != nil {
			f.Flush()
		}
	}
}

// googleSimpleStreamWithUsage emits a stop with a populated
// usageMetadata block. The provider folds usageMetadata into the
// final-chunk Usage{}.
func googleSimpleStreamWithUsage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write(
			[]byte(
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}` + "\n\n",
			),
		)
		if f != nil {
			f.Flush()
		}
	}
}

// googleToolCallStream emits a functionCall part. Gemini puts tool
// calls inside the candidate's content.parts; the provider extracts
// them and surfaces a CompletionChunk.ToolCalls.
func googleToolCallStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = w.Write(
			[]byte(
				`data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"echo","args":{"text":"hi"}}}]},"finishReason":"STOP"}]}` + "\n\n",
			),
		)
		if f != nil {
			f.Flush()
		}
	}
}
