package runner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestRunAccumulatesCompletedContinuationItemsInOrder(t *testing.T) {
	first := []byte("first")
	second := []byte("second")
	client := runnertest.NewClient([][]llm.CompletionChunk{{
		{Content: "answer", CompletedItems: []llm.ContinuationItem{{OutputIndex: llm.OutputPosition(0), Provider: "p", Format: "f", Data: first}}},
		{CompletedItems: []llm.ContinuationItem{{OutputIndex: llm.OutputPosition(2), Provider: "p", Format: "f", Data: second}}},
	}})

	r := runner.New(client, runner.WithMaxIterations(1))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	assistant := result.Messages[len(result.Messages)-1]
	if len(assistant.ContinuationItems) != 2 || assistant.ContinuationItems[0].OutputIndex == nil || *assistant.ContinuationItems[0].OutputIndex != 0 || assistant.ContinuationItems[1].OutputIndex == nil || *assistant.ContinuationItems[1].OutputIndex != 2 {
		t.Fatalf("continuation items = %#v", assistant.ContinuationItems)
	}

	first[0], second[0] = 'X', 'X'
	if string(assistant.ContinuationItems[0].Data) != "first" || string(assistant.ContinuationItems[1].Data) != "second" {
		t.Fatalf("runner retained borrowed data: %#v", assistant.ContinuationItems)
	}
}
