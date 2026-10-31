package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ErrLiveBudget reports the trial's request, reported-token, or stream-size limit.
var ErrLiveBudget = errors.New("live swarm request budget exhausted")

// Shared by per-request admission, goal activation, and the reported policy.
const liveReportedTokenLimit = 100_000

// LiveUsage records actual attempted requests and provider-reported usage. A
// request without usage is counted separately; totals may therefore be partial.
type LiveUsage struct {
	Requests         int `json:"requests"`
	UsageReports     int `json:"usage_reports"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens"`
}

type liveMeter struct {
	mu       sync.Mutex
	requests int
	tokens   int
	roles    map[string]LiveUsage
	verified bool
	finish   context.CancelCauseFunc
	failures []error
}

type liveClient struct {
	agent LiveAgent
	meter *liveMeter
}

func (c liveClient) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if err := context.Cause(ctx); err != nil {
			yield(llm.CompletionChunk{}, err)
			return
		}
		c.meter.mu.Lock()
		if c.meter.verified {
			c.meter.mu.Unlock()
			yield(llm.CompletionChunk{}, errLiveVerified)
			return
		}
		if c.meter.requests >= 36 || c.meter.tokens >= liveReportedTokenLimit {
			c.meter.failures = append(c.meter.failures, ErrLiveBudget)
			c.meter.mu.Unlock()
			yield(llm.CompletionChunk{}, ErrLiveBudget)
			return
		}
		c.meter.requests++
		stats := c.meter.roles[c.agent.Role]
		stats.Requests++
		c.meter.roles[c.agent.Role] = stats
		c.meter.mu.Unlock()

		callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		var usage llm.Usage
		reported, size := false, 0
		c.agent.Provider.Complete(callCtx, request)(func(chunk llm.CompletionChunk, err error) bool {
			c.meter.recordFailure(callCtx, err)
			if chunk.UsageReported {
				usage, reported = chunk.Usage, true
			}
			size += len(chunk.Content) + len(chunk.Thinking)
			for _, call := range chunk.ToolCalls {
				size += len(call.Function.Arguments)
			}
			for _, item := range chunk.CompletedItems {
				size += len(item.Data)
			}
			if size > 64<<10 {
				c.meter.recordFailure(callCtx, ErrLiveBudget)
				cancel()
				yield(llm.CompletionChunk{}, ErrLiveBudget)
				return false
			}
			return yield(chunk, err)
		})
		if reported {
			c.meter.mu.Lock()
			stats := c.meter.roles[c.agent.Role]
			stats.UsageReports++
			stats.PromptTokens += usage.PromptTokens
			stats.CompletionTokens += usage.CompletionTokens
			stats.CachedTokens += usage.CachedTokens
			total := max(usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
			stats.TotalTokens += total
			c.meter.tokens += total
			c.meter.roles[c.agent.Role] = stats
			c.meter.mu.Unlock()
		}
	}
}
