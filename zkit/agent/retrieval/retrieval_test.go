package retrieval_test

import (
	"context"
	"strings"
	"testing"

	agentretrieval "github.com/zarldev/zarlmono/zkit/agent/retrieval"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	airetrieval "github.com/zarldev/zarlmono/zkit/ai/retrieval"
)

func TestPromptSourceRetrievesQuery(t *testing.T) {
	src := agentretrieval.PromptSource{Retriever: airetrieval.RetrieverFunc(func(_ context.Context, q string, _ ...airetrieval.RetrieveOption) ([]airetrieval.Document, error) {
		if q != "what" {
			t.Fatalf("query = %q", q)
		}
		return []airetrieval.Document{{Text: "answer", Score: 0.5}}, nil
	}), Format: agentretrieval.FormatOptions{ShowScores: true}}
	body, err := src.System(t.Context(), runner.PromptVars{"query": "what"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "answer") || !strings.Contains(body, "score=0.5000") {
		t.Fatalf("body = %q", body)
	}
}
