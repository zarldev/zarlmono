package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type cacheUsageClient struct {
	attempt int
	usage   []llm.Usage
}

func (c *cacheUsageClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		attempt := c.attempt
		c.attempt++
		if !yield(llm.CompletionChunk{Usage: c.usage[attempt], UsageReported: true}, nil) {
			return
		}
		if attempt == 0 {
			yield(llm.CompletionChunk{}, errors.New("Failed to parse tool call arguments as JSON: retry"))
			return
		}
		yield(runnertest.ChunkText("done"), nil)
	}
}

func TestCacheUsageSumsAcrossProviderAttempts(t *testing.T) {
	first := llm.Usage{
		PromptTokens:          30,
		CompletionTokens:      4,
		TotalTokens:           34,
		CachedTokens:          3,
		CacheCreationTokens:   20,
		CacheCreation5mTokens: 7,
	}
	second := llm.Usage{
		PromptTokens:          40,
		CompletionTokens:      5,
		TotalTokens:           45,
		CachedTokens:          4,
		CacheCreationTokens:   30,
		CacheCreation1hTokens: 11,
	}
	client := &cacheUsageClient{usage: []llm.Usage{first, second}}
	result := runner.New(client).Run(t.Context(), runner.TaskSpec{Prompt: "question"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}
	want := llm.Usage{
		PromptTokens:          70,
		CompletionTokens:      9,
		TotalTokens:           79,
		CachedTokens:          7,
		CacheCreationTokens:   50,
		CacheCreation5mTokens: 7,
		CacheCreation1hTokens: 11,
	}
	if result.TotalUsage == nil || *result.TotalUsage != want {
		t.Fatalf("TotalUsage = %+v, want %+v", result.TotalUsage, want)
	}

	result.TotalUsage.CacheCreationTokens = 999
	if first.CacheCreationTokens != 20 || second.CacheCreationTokens != 30 {
		t.Fatal("total usage aliases provider attempt input")
	}
}
