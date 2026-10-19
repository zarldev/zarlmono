package runner_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type contentIdentitySink struct {
	runner.NopSink
	contents []runner.Content
}

func (s *contentIdentitySink) OnContent(_ context.Context, content runner.Content) {
	s.contents = append(s.contents, content)
}

func TestRunnerPublishesContentOutputIdentityPerChunk(t *testing.T) {
	zero, one := 0, 1
	client := runnertest.NewClient([][]llm.CompletionChunk{{
		{Content: "first", ContentOutputIndex: &zero},
		{Content: " unknown"},
		{Content: " second", ContentOutputIndex: &one},
	}})
	sink := &contentIdentitySink{}

	result := runner.New(client, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{
		ID:     "task",
		Depth:  2,
		Prompt: "test",
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v", result.Err)
	}
	if result.FinalContent != "first unknown second" {
		t.Fatalf("FinalContent = %q, want flattened content", result.FinalContent)
	}

	zero, one = 8, 9
	if len(sink.contents) != 3 {
		t.Fatalf("content events = %d, want 3", len(sink.contents))
	}
	wantDeltas := []string{"first", " unknown", " second"}
	wantIndices := []*int{intPointer(0), nil, intPointer(1)}
	for i, event := range sink.contents {
		if event.TaskID != "task" || event.Depth != 2 || event.Delta != wantDeltas[i] {
			t.Errorf("event[%d] = %#v", i, event)
		}
		if !equalOptionalInt(event.ContentOutputIndex, wantIndices[i]) {
			t.Errorf("event[%d].ContentOutputIndex = %v, want %v", i, event.ContentOutputIndex, wantIndices[i])
		}
	}
}

func intPointer(value int) *int { return &value }

func equalOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
