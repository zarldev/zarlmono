package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// streamResult is the accumulated outcome of draining one completion stream.
type streamResult struct {
	content       string
	thinking      string
	toolCalls     map[string]*llm.ToolCall
	toolCallOrder []string
	usage         *llm.Usage
	err           error
	accepted      bool
}

// drainStream directly and synchronously consumes one completion stream. The
// stream wrapper owns idle observation; this consumer owns every value retained
// after a yield returns.
func (r *Runner) drainStream(
	ctx, streamCtx context.Context,
	cancelStream context.CancelCauseFunc,
	spec TaskSpec,
	stream llm.CompletionStream,
) streamResult {
	var contentBuilder strings.Builder
	var thinkingBuilder strings.Builder
	toolCalls := map[string]*llm.ToolCall{}
	var toolCallOrder []string
	var streamErr error
	var iterUsage *llm.Usage
	accepted := false

	stream(func(chunk llm.CompletionChunk, err error) bool {
		if err != nil {
			streamErr = err
			return false
		}
		accepted = true
		if chunk.UsageReported {
			usage := chunk.Usage
			iterUsage = &usage
		}
		if chunk.Content != "" {
			contentBuilder.WriteString(chunk.Content)
			r.publishContentChunk(ctx, spec, chunk.Content)
		}
		if chunk.Thinking != "" {
			thinkingBuilder.WriteString(chunk.Thinking)
			r.publishThinkingChunk(ctx, spec, chunk.Thinking)
			if r.thinkingBudgetBytes > 0 && contentBuilder.Len() == 0 && len(toolCallOrder) == 0 &&
				thinkingBuilder.Len() > r.thinkingBudgetBytes {
				streamErr = fmt.Errorf("%w (%d bytes of thinking, no output)", ErrThinkingBudget, thinkingBuilder.Len())
				return false
			}
		}
		for _, tc := range chunk.ToolCalls {
			existing, ok := toolCalls[tc.ID]
			if !ok {
				id := strings.Clone(tc.ID)
				existing = &llm.ToolCall{ID: id, Type: strings.Clone(tc.Type)}
				toolCalls[id] = existing
				toolCallOrder = append(toolCallOrder, id)
			}
			if tc.Function.Name != "" {
				existing.Function.Name = strings.Clone(tc.Function.Name)
			}
			if tc.Function.Arguments != "" {
				existing.Function.Arguments += strings.Clone(tc.Function.Arguments)
			}
		}
		return true
	})

	cause := context.Cause(streamCtx)
	if cause != nil {
		switch {
		case errors.Is(cause, ErrStreamIdle):
			streamErr = ErrStreamIdle
		case errors.Is(cause, ErrIterationTimeout):
			streamErr = ErrIterationTimeout
		default:
			streamErr = fmt.Errorf("%w: %w", ErrCancelled, cause)
		}
	}
	if streamErr == nil && context.Cause(ctx) != nil {
		streamErr = fmt.Errorf("%w: %w", ErrCancelled, context.Cause(ctx))
	}
	if streamErr != nil && !accepted && isEmptyStreamDecodeError(streamErr) {
		streamErr = fmt.Errorf("%w: %w", ErrEmptyStream, streamErr)
	}
	cancelStream(nil)

	return streamResult{
		content:       contentBuilder.String(),
		thinking:      thinkingBuilder.String(),
		toolCalls:     toolCalls,
		toolCallOrder: toolCallOrder,
		usage:         iterUsage,
		err:           streamErr,
		accepted:      accepted,
	}
}
