package runner_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRunner_TurnQualityPrecedesCompletionGate(t *testing.T) {
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{{}},
		{runnertest.ChunkText("done without editing")},
		{runnertest.ChunkText("still done without editing")},
	})
	r := runner.New(client,
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(4),
		runner.WithTurnQuality(runner.EmptyResponseDetector{
			Message:        "quality correction",
			MaxCorrections: 1,
		}),
		runner.WithCompletionGate(runner.RequireWork{
			Message:        "work correction",
			MaxCorrections: 1,
		}),
	)

	res := r.Run(t.Context(), runner.TaskSpec{Prompt: "make the change"})
	if res.Err != nil || res.Reason != runner.TerminalCompleted {
		t.Fatalf("Run = %q, %v; want completed, nil", res.Reason, res.Err)
	}
	if got := client.CallCount(); got != 3 {
		t.Fatalf("provider calls = %d, want 3", got)
	}

	qualityAt, workAt := -1, -1
	qualityCount, workCount := 0, 0
	for i, message := range res.Messages {
		if strings.Contains(message.Content, "quality correction") {
			qualityAt = i
			qualityCount++
		}
		if strings.Contains(message.Content, "work correction") {
			workAt = i
			workCount++
		}
	}
	if qualityCount != 1 || workCount != 1 || qualityAt >= workAt {
		t.Fatalf("correction order quality=(%d at %d), work=(%d at %d)", qualityCount, qualityAt, workCount, workAt)
	}
}
