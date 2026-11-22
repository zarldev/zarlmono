package rewind_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestIndexedTextFallbackRemainsCheckpointable(t *testing.T) {
	t.Parallel()
	for _, indexed := range []bool{false, true} {
		name := "ordinary text fallback"
		if indexed {
			name = "indexed provider text"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			const text = `{"name":"echo","arguments":{}}`
			chunk := runnertest.ChunkText(text)
			if indexed {
				chunk.ContentOutputIndex = llm.OutputPosition(2)
			}
			client := runnertest.NewClient([][]llm.CompletionChunk{{chunk}, {runnertest.ChunkText("done")}})
			sink := runnertest.NewSink()
			r := runner.New(client, runner.WithSink(sink), runner.WithMaxIterations(2),
				runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "echoed"})))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			encoded, err := rewind.EncodeResume(1, result.Messages,
				rewind.Target{Provider: "openai-codex", Model: "gpt-6-astra"}, "turn", 1, rewind.InitialContinuation{})
			if err != nil {
				t.Fatalf("save runner result after %d tool dispatches: %v", sink.ToolStartedCount(), err)
			}
			decoded, err := rewind.DecodeResume(encoded)
			if err != nil || !reflect.DeepEqual(decoded.Context, result.Messages) {
				t.Fatalf("exact save/resume changed context: %v", err)
			}
			wantCalls := 1
			if indexed {
				wantCalls = 0
				if len(result.Messages) != 2 || result.Messages[1].Content != text || result.Messages[1].ContentOutputIndex == nil || *result.Messages[1].ContentOutputIndex != 2 {
					t.Fatal("indexed source text or output position changed")
				}
			}
			if sink.ToolStartedCount() != wantCalls {
				t.Fatalf("tool dispatches = %d, want %d", sink.ToolStartedCount(), wantCalls)
			}
		})
	}
}

func TestCodexIndexedTextWithoutNativeItemRemainsCheckpointable(t *testing.T) {
	t.Parallel()
	const text = `{"name":"echo","arguments":{}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":2,\"delta\":%q}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", text)
	}))
	t.Cleanup(server.Close)
	provider := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "fixture", AccountID: "fixture", Expires: time.Now().Add(time.Hour)}},
		openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
	sink := runnertest.NewSink()
	r := runner.New(runner.ClientFromProvider(provider), runner.WithSink(sink), runner.WithMaxIterations(1),
		runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "echoed"})))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	encoded, err := rewind.EncodeResume(1, result.Messages,
		rewind.Target{Provider: "openai-codex", Model: "gpt-6-astra"}, "turn", 1, rewind.InitialContinuation{})
	if err != nil {
		t.Fatalf("Codex result cannot be saved: %v", err)
	}
	decoded, err := rewind.DecodeResume(encoded)
	if err != nil || !reflect.DeepEqual(decoded.Context, result.Messages) {
		t.Fatalf("Codex replay bytes changed: %v", err)
	}
	if sink.ToolStartedCount() != 0 || result.FinalContent != text || result.Reason != runner.TerminalCompleted {
		t.Fatalf("indexed text invoked a fallback: calls=%d reason=%v", sink.ToolStartedCount(), result.Reason)
	}
}
