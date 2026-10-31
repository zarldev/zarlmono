package openaicodex_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestConversationCacheRouteMatchesCodexBodyAndHeaders(t *testing.T) {
	backend := newCodexBackend(t, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: "+`{"type":"response.output_text.delta","delta":"done"}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{}}}`+"\n\n")
	})
	t.Cleanup(backend.Close)
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "acct")}, openaicodex.WithBaseURL(backend.srv.URL), openaicodex.WithNoRetry())
	for _, route := range []string{"session-a", "session-b"} {
		r := runner.New(runner.ClientFromProvider(provider), runner.WithPromptCacheKey(route))
		for _, id := range []taskscope.ID{"turn-1", "turn-2"} {
			result := r.Run(t.Context(), runner.TaskSpec{ID: id, Prompt: "hello"})
			if result.Err != nil || result.ID != id {
				t.Fatalf("Run = %+v", result)
			}
			if backend.lastBody["prompt_cache_key"] != route {
				t.Errorf("body cache route = %v, want %s", backend.lastBody["prompt_cache_key"], route)
			}
			for _, header := range []string{"Session_id", "Conversation_id"} {
				if got := backend.lastHeader.Get(header); got != route {
					t.Errorf("%s = %s, want %s", header, got, route)
				}
			}
			if backend.lastBody["store"] != false {
				t.Error("cache routing enabled server-side storage")
			}
		}
	}
}
