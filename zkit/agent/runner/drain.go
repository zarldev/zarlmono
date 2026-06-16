package runner

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// streamResult is the accumulated outcome of draining one completion stream:
// the assistant's visible content and reasoning, the tool calls it emitted
// (in emission order), the last provider-reported usage seen this iteration
// (nil if the provider reported none), and a terminal stream error (nil on a
// clean EOF). The caller folds usage into the run-wide totals and decides
// terminal control flow from err.
type streamResult struct {
	content       string
	thinking      string
	toolCalls     map[string]*llm.ToolCall
	toolCallOrder []string
	usage         *llm.Usage
	err           error
}

// drainStream consumes one completion stream to a clean EOF or a terminal
// condition (provider error, iteration timeout, or stream-idle watchdog) and
// classifies why it ended. It owns the per-iteration context for the duration
// of the drain: it starts the idle watchdog and the producer goroutine, and
// calls cancelIter before returning. cancelIter is idempotent, so callers may
// cancel again on later control-flow paths.
func (r *Runner) drainStream(
	ctx, iterCtx context.Context,
	cancelIter context.CancelFunc,
	spec TaskSpec,
	iterNum int,
	stream iter.Seq2[llm.CompletionChunk, error],
) streamResult {
	var contentBuilder strings.Builder
	var thinkingBuilder strings.Builder
	toolCalls := map[string]*llm.ToolCall{}
	var toolCallOrder []string
	var streamErr error
	// iterUsage holds this iteration's provider-reported usage — returned
	// so the caller folds it into the run-wide total exactly once. The
	// caller, not this method, updates the cross-iteration lastUsage so the
	// "retain prior value when this stream carried no usage" semantics stay
	// in one place.
	var iterUsage *llm.Usage

	// Idle-detection watchdog (optional). When streamIdleTimeout is set, a
	// goroutine watches the lastChunk timestamp and cancels iterCtx if the
	// stream stops producing chunks for longer than the budget. Catches "LLM
	// stream hung mid-response" without affecting fast-streaming runs.
	var lastChunk atomicTime
	lastChunk.Set(time.Now())
	if r.timeouts.streamIdle > 0 {
		go watchStreamIdle(iterCtx, cancelIter, &lastChunk, r.timeouts.streamIdle)
	}

	// Forward the stream's range-over-func into a buffered channel so the
	// consumer below can `select` on (channel, iterCtx.Done()). Without this
	// layer, a stream that never yields ANY chunk (provider's iter.Seq2
	// blocked on an HTTP body that doesn't respect ctx cancellation) leaves
	// the consumer stuck in the for-range body for as long as the underlying
	// read blocks — the watchdog cancelIter has no effect because the
	// goroutine is parked outside Go's view. The producer-goroutine pattern
	// makes the block externally observable: when iterCtx hits its deadline,
	// the select fires immediately, we leak the producer goroutine (the leak
	// ends when the HTTP body finally closes or the process exits), and the
	// iteration records a clean timeout. Bug surfaced on Qwen+hugo-12448: 12
	// min of zero progress, no chunks ever, SIGKILL from outer ctx.
	type chunkOrErr struct {
		chunk llm.CompletionChunk
		err   error
	}
	ch := make(chan chunkOrErr, 16)
	go func() {
		defer close(ch)
		for chunk, cerr := range stream {
			select {
			case ch <- chunkOrErr{chunk, cerr}:
			case <-iterCtx.Done():
				return
			}
		}
	}()

drain:
	for {
		select {
		case <-iterCtx.Done():
			break drain
		case got, ok := <-ch:
			if !ok {
				break drain
			}
			lastChunk.Set(time.Now())
			if got.err != nil {
				streamErr = got.err
				break drain
			}
			chunk := got.chunk
			if chunk.Usage != nil {
				iterUsage = chunk.Usage
			}
			if chunk.Content != "" {
				contentBuilder.WriteString(chunk.Content)
				r.publishContentChunk(ctx, spec, chunk.Content)
			}
			if chunk.Thinking != "" {
				thinkingBuilder.WriteString(chunk.Thinking)
				r.publishThinkingChunk(ctx, spec, chunk.Thinking)
				// Thinking-only budget: a turn that has streamed only
				// reasoning past the byte budget — no content, no tool call —
				// is the stuck-thinking loop. Cut it now (content-aware, so a
				// healthy turn that's emitted real output is never touched);
				// the run loop recovers with a "stop reasoning, act" nudge.
				if r.thinkingBudgetBytes > 0 && contentBuilder.Len() == 0 && len(toolCallOrder) == 0 &&
					thinkingBuilder.Len() > r.thinkingBudgetBytes {
					streamErr = fmt.Errorf("%w (%d bytes of thinking, no output)", ErrThinkingBudget, thinkingBuilder.Len())
					cancelIter()
					break drain
				}
			}
			for _, tc := range chunk.ToolCalls {
				existing, ok := toolCalls[tc.ID]
				if !ok {
					existing = &llm.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
					}
					toolCalls[tc.ID] = existing
					toolCallOrder = append(toolCallOrder, tc.ID)
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}
	}

	slog.InfoContext(ctx, "runner: drain done", "task", string(spec.ID), "iter", iterNum,
		"chunks_content_bytes", contentBuilder.Len(),
		"tool_calls", len(toolCallOrder),
		"stream_err", streamErr,
		"iter_ctx_err", iterCtx.Err(),
		"outer_ctx_err", ctx.Err(),
		"idle_for_ms", time.Since(lastChunk.Get()).Milliseconds())

	// Classify *before* the manual cancelIter — the iterCtx.Err() check
	// needs to distinguish "watchdog or outer-ctx fired" from "we cancelled
	// it as part of normal cleanup". After cancelIter() is called,
	// iterCtx.Err() always reads context.Canceled regardless of what
	// happened during the stream, so the classification has to happen first.
	if streamErr == nil && iterCtx.Err() != nil && ctx.Err() == nil {
		// Outer ctx is fine, inner one died on its own — either the
		// iteration timeout or the stream-idle watchdog.
		idle := lastChunk.Get()
		switch {
		case r.timeouts.streamIdle > 0 && time.Since(idle) >= r.timeouts.streamIdle:
			streamErr = fmt.Errorf("%w (no chunks for %s)", ErrStreamIdle, time.Since(idle).Round(time.Second))
		case r.timeouts.iteration > 0:
			streamErr = fmt.Errorf("%w (%s budget)", ErrIterationTimeout, r.timeouts.iteration)
		default:
			streamErr = fmt.Errorf("%w: %w", ErrCancelled, iterCtx.Err())
		}
	}
	if streamErr == nil && ctx.Err() != nil {
		streamErr = fmt.Errorf("%w: %w", ErrCancelled, ctx.Err())
	}
	// A provider error on a stream that produced no content AND no tool
	// calls is the empty-stream gateway-cut scenario (see ErrEmptyStream):
	// the connection opened, emitted nothing, then closed with an
	// EOF-class decode error. Tag it with the sentinel — keeping the
	// original error wrapped for the logs — so the run loop can errors.Is
	// it and retry instead of terminating. The zero-content/zero-tool
	// guard keeps a mid-output decode failure from being reclassified.
	if streamErr != nil && contentBuilder.Len() == 0 && len(toolCallOrder) == 0 &&
		isEmptyStreamDecodeError(streamErr) {
		streamErr = fmt.Errorf("%w: %w", ErrEmptyStream, streamErr)
	}
	cancelIter()

	return streamResult{
		content:       contentBuilder.String(),
		thinking:      thinkingBuilder.String(),
		toolCalls:     toolCalls,
		toolCallOrder: toolCallOrder,
		usage:         iterUsage,
		err:           streamErr,
	}
}
