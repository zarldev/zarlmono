package runner_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

type keepRecordingCompactor struct {
	keeps []int
}

func (c *keepRecordingCompactor) Compact(_ context.Context, history []llm.Message, keep int) (compact.Result, error) {
	c.keeps = append(c.keeps, keep)
	return compact.Result{History: history}, nil
}

func TestCompactionKeepPolicy(t *testing.T) {
	tests := []struct {
		name     string
		usage    llm.Usage
		opts     []options.Option[runner.Runner]
		wantKeep int
	}{
		{
			name:     "static without pressure",
			usage:    llm.Usage{PromptTokens: 999_999},
			opts:     []options.Option[runner.Runner]{runner.WithCompactKeepRecent(4)},
			wantKeep: 4,
		},
		{
			name:  "adaptive overrides static",
			usage: llm.Usage{PromptTokens: 50},
			opts: []options.Option[runner.Runner]{
				runner.WithCompactKeepRecent(2),
				runner.WithAdaptiveKeepRecent(1, 3, 3),
			},
			wantKeep: 3,
		},
		{
			name:  "pressure under threshold leaves keep",
			usage: llm.Usage{PromptTokens: 50},
			opts: []options.Option[runner.Runner]{
				runner.WithCompactKeepRecent(6),
				runner.WithTokenPressureCompact(100, 0.8),
			},
			wantKeep: 6,
		},
		{
			name:  "pressure over threshold shrinks keep",
			usage: llm.Usage{PromptTokens: 85},
			opts: []options.Option[runner.Runner]{
				runner.WithCompactKeepRecent(6),
				runner.WithTokenPressureCompact(100, 0.8),
			},
			wantKeep: 1,
		},
		{
			name:  "pressure falls back to total tokens",
			usage: llm.Usage{TotalTokens: 90},
			opts: []options.Option[runner.Runner]{
				runner.WithCompactKeepRecent(6),
				runner.WithTokenPressureCompact(100, 0.8),
			},
			wantKeep: 1,
		},
		{
			name:  "disabled pressure leaves keep",
			usage: llm.Usage{PromptTokens: 999_999},
			opts: []options.Option[runner.Runner]{
				runner.WithCompactKeepRecent(6),
				runner.WithTokenPressureCompact(0, 0.8),
			},
			wantKeep: 6,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := runnertest.NewClient([][]llm.CompletionChunk{
				{
					runnertest.ChunkToolCall("c1", "echo", `{}`),
					{UsageReported: true, Usage: test.usage},
				},
				{runnertest.ChunkText("done")},
			})
			compactor := &keepRecordingCompactor{}
			registry := tools.NewRegistry(runnertest.Tool{Name: "echo", Description: "echo", Result: "result"})
			runnerOptions := []options.Option[runner.Runner]{
				runner.WithTools(registry),
				runner.WithCompactor(compactor),
			}
			runnerOptions = append(runnerOptions, test.opts...)
			r := runner.New(client, runnerOptions...)

			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"})
			if result.Err != nil {
				t.Fatalf("Run: %v", result.Err)
			}
			if len(compactor.keeps) != 1 {
				t.Fatalf("Compact calls = %d, want 1", len(compactor.keeps))
			}
			if got := compactor.keeps[0]; got != test.wantKeep {
				t.Errorf("Compact keep = %d, want %d", got, test.wantKeep)
			}
		})
	}
}
