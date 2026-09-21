package runner_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAssistantHistoryPreservesWhitespace(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"\n  answer\n\n", "\t\n ", "\u2003answer\u00a0"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkText(text)}})
			result := runner.New(client).Run(t.Context(), runner.TaskSpec{Prompt: "answer"})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if len(result.Messages) != 2 || result.Messages[1].Content != text {
				t.Fatalf("assistant history did not preserve streamed text: %+v", result.Messages)
			}
			if result.FinalContent != strings.TrimSpace(text) {
				t.Fatalf("final content = %q, want trimmed presentation", result.FinalContent)
			}
		})
	}
}
