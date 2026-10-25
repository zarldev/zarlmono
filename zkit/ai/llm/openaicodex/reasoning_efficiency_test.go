package openaicodex_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestDefaultEffortHonorsSummaryOverrideAndReasoningUsage(t *testing.T) {
	backend := newCodexBackend(t, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":10,"total_tokens":15,"output_tokens_details":{"reasoning_tokens":7}}}}`+"\n\n")
	})
	t.Cleanup(backend.Close)
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: freshToken(t, "acct")}, openaicodex.WithBaseURL(backend.srv.URL), openaicodex.WithModel("gpt-5.6"), openaicodex.WithNoRetry())
	var usage llm.Usage
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: llm.ModelOptions{"reasoning_summary": "auto", "parallel_tool_calls": false}}) {
		if err != nil {
			t.Fatal(err)
		}
		if chunk.UsageReported {
			usage = chunk.Usage
		}
	}
	if value, present := backend.lastBody["parallel_tool_calls"]; !present || value != false {
		t.Errorf("explicit parallel_tool_calls false was lost: %v", backend.lastBody)
	}
	reasoning, ok := backend.lastBody["reasoning"].(map[string]any)
	if !ok || reasoning["summary"] != "auto" {
		t.Errorf("reasoning = %v", reasoning)
	}
	if !usage.ReasoningTokensReported || usage.ReasoningTokens != 7 || usage.CompletionTokens != 10 {
		t.Errorf("usage = %+v", usage)
	}
}
