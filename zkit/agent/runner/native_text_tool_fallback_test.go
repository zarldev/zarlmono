package runner_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const nativeTextToolArtifact = `{"name":"mutate","arguments":{}}`

func TestRunGoogleNativeTextDoesNotDispatchFallback(t *testing.T) {
	t.Parallel()
	for _, signed := range []bool{false, true} {
		t.Run(fmt.Sprintf("signed=%t", signed), func(t *testing.T) {
			t.Parallel()
			text := "  " + nativeTextToolArtifact + "\n"
			part := map[string]any{"text": text, "partMetadata": map[string]any{"original": "retained"}}
			if signed {
				part["thoughtSignature"] = "c2ln"
			}
			parts, err := json.Marshal([]map[string]any{part})
			if err != nil {
				t.Fatal(err)
			}
			provider, requests := nativeTextFallbackProvider(t, []string{string(parts), `[{"text":"next answer"}]`})
			tool := &countingTool{name: "mutate", mutates: true}
			sink := runnertest.NewSink()
			r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink), runner.WithMaxIterations(3))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
			// Check side effects even when the pre-fix runner has already failed
			// preparing the next request after executing the synthetic call.
			if tool.calls != 0 || sink.ToolStartedCount() != 0 {
				t.Errorf("native text admitted execution: calls=%d, starts=%d", tool.calls, sink.ToolStartedCount())
			}
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if result.Reason != runner.TerminalCompleted || result.Iterations != 1 || result.FinalContent != strings.TrimSpace(text) {
				t.Fatalf("native text result = %+v", result)
			}
			if sink.ContentText() != text || len(result.Messages) != 2 {
				t.Fatalf("display=%q, messages=%#v", sink.ContentText(), result.Messages)
			}
			assistant := result.Messages[1]
			if assistant.Content != text || len(assistant.ToolCalls) != 0 || len(assistant.ContinuationItems) != 1 {
				t.Fatalf("native text history = %#v", assistant)
			}
			item := assistant.ContinuationItems[0]
			assertNativeFallbackJSON(t, item.Data, part)
			originalBytes := bytes.Clone(item.Data)
			firstRequest := takeNativeFallbackRequest(t, requests)
			if len(firstRequest) != 1 {
				t.Fatalf("initial contents = %#v", firstRequest)
			}

			next := r.Run(t.Context(), runner.TaskSpec{Prompt: "continue", Context: result.Messages})
			if next.Err != nil {
				t.Fatal(next.Err)
			}
			if next.Reason != runner.TerminalCompleted || next.FinalContent != "next answer" || tool.calls != 0 || sink.ToolStartedCount() != 0 {
				t.Fatalf("subsequent turn = %+v, executions=%d", next, tool.calls)
			}
			replayed := takeNativeFallbackRequest(t, requests)
			if len(replayed) != 3 || replayed[1].Role != "model" || len(replayed[1].Parts) != 1 {
				t.Fatalf("replayed contents = %#v", replayed)
			}
			assertNativeFallbackJSON(t, replayed[1].Parts[0], part)
			if !bytes.Equal(result.Messages[1].ContinuationItems[0].Data, originalBytes) || !bytes.Equal(next.Messages[1].ContinuationItems[0].Data, originalBytes) {
				t.Fatal("replay changed original native bytes")
			}
		})
	}
}

func TestRunTextToolFallbackRequiresNoNativeContinuation(t *testing.T) {
	t.Parallel()
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native=%t", native), func(t *testing.T) {
			t.Parallel()
			chunk := runnertest.ChunkText(nativeTextToolArtifact)
			if native {
				// Opaque replay must gate fallback without interpreting a provider
				// name, format, kind, or payload as permission to synthesize calls.
				chunk.CompletedItems = []llm.ContinuationItem{{Provider: "opaque", Format: "future", Kind: "reasoning", Data: []byte(`{"opaque":true}`)}}
			}
			client := runnertest.NewClient([][]llm.CompletionChunk{{chunk}, {runnertest.ChunkText("done")}})
			tool := &countingTool{name: "mutate", mutates: true}
			sink := runnertest.NewSink()
			r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink), runner.WithMaxIterations(3))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if result.Reason != runner.TerminalCompleted {
				t.Fatalf("reason = %v", result.Reason)
			}
			if native {
				if tool.calls != 0 || sink.ToolStartedCount() != 0 || client.CallCount() != 1 || result.FinalContent != nativeTextToolArtifact {
					t.Fatalf("opaque native fallback executed: calls=%d, starts=%d, completions=%d, content=%q", tool.calls, sink.ToolStartedCount(), client.CallCount(), result.FinalContent)
				}
				if result.Messages[1].Content != nativeTextToolArtifact || len(result.Messages[1].ToolCalls) != 0 || !bytes.Equal(result.Messages[1].ContinuationItems[0].Data, chunk.CompletedItems[0].Data) {
					t.Fatalf("opaque native history = %#v", result.Messages[1])
				}
				return
			}
			if tool.calls != 1 || sink.ToolStartedCount() != 1 || client.CallCount() != 2 || result.FinalContent != "done" {
				t.Fatalf("ordinary fallback lost: calls=%d, starts=%d, completions=%d, content=%q", tool.calls, sink.ToolStartedCount(), client.CallCount(), result.FinalContent)
			}
			if len(result.Messages) != 4 || len(result.Messages[1].ToolCalls) != 1 || result.Messages[2].Role != llm.RoleTool || result.Messages[2].ToolCallID != result.Messages[1].ToolCalls[0].ID {
				t.Fatalf("ordinary fallback history = %#v", result.Messages)
			}
		})
	}
}

func TestRunGoogleStructuredNativeToolContinues(t *testing.T) {
	t.Parallel()
	// A textual artifact alongside a real signed call must remain display
	// text: execute only the structured call and replay the original parts.
	parts := `[{"text":` + fmt.Sprintf("%q", nativeTextToolArtifact) + `},{"functionCall":{"id":"native-call","name":"mutate"},"thoughtSignature":"c2ln"}]`
	provider, requests := nativeTextFallbackProvider(t, []string{parts, `[{"text":"done"}]`})
	tool := &countingTool{name: "mutate", mutates: true}
	sink := runnertest.NewSink()
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink), runner.WithMaxIterations(3))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "start"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != runner.TerminalCompleted || result.FinalContent != "done" || result.Iterations != 2 || tool.calls != 1 || sink.ToolStartedCount() != 1 {
		t.Fatalf("structured result = %+v, executions=%d, starts=%d", result, tool.calls, sink.ToolStartedCount())
	}
	if len(result.Messages) != 4 || len(result.Messages[1].ToolCalls) != 1 || result.Messages[2].Role != llm.RoleTool || result.Messages[2].ToolCallID != "native-call" {
		t.Fatalf("structured history = %#v", result.Messages)
	}
	_ = takeNativeFallbackRequest(t, requests)
	replayed := takeNativeFallbackRequest(t, requests)
	if len(replayed) != 3 || len(replayed[1].Parts) != 2 || len(replayed[2].Parts) != 1 {
		t.Fatalf("structured replay contents = %#v", replayed)
	}
	var wantParts []map[string]any
	if err := json.Unmarshal([]byte(parts), &wantParts); err != nil {
		t.Fatal(err)
	}
	for index, want := range wantParts {
		assertNativeFallbackJSON(t, replayed[1].Parts[index], want)
	}
	assertNativeFallbackJSON(t, replayed[2].Parts[0], map[string]any{"functionResponse": map[string]any{"id": "native-call", "name": "mutate", "response": map[string]any{"result": "ran"}}})
}

func nativeTextFallbackProvider(t *testing.T, turns []string) (*google.Provider, <-chan []byte) {
	t.Helper()
	requests := make(chan []byte, 4)
	var index atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		requests <- body
		turn := int(index.Add(1)) - 1
		if turn >= len(turns) {
			http.Error(w, "unexpected completion", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":%s},\"finishReason\":\"STOP\"}]}\n\n", turns[turn])
	}))
	t.Cleanup(server.Close)
	provider, err := google.NewProvider("test-key", google.WithBaseURL(server.URL), google.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return provider, requests
}

type nativeFallbackContent struct {
	Role  string            `json:"role"`
	Parts []json.RawMessage `json:"parts"`
}

func takeNativeFallbackRequest(t *testing.T, requests <-chan []byte) []nativeFallbackContent {
	t.Helper()
	select {
	case body := <-requests:
		var request struct {
			Contents []nativeFallbackContent `json:"contents"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		return request.Contents
	default:
		t.Fatal("missing Google HTTP request")
		return nil
	}
}

func assertNativeFallbackJSON(t *testing.T, data []byte, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("native replay (-want +got):\n%s", diff)
	}
}
