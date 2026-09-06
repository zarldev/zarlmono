package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// streamResult is the accumulated outcome of draining one completion stream.
type streamResult struct {
	content            string
	thinking           string
	contentOutputIndex *int
	continuationItems  []llm.ContinuationItem
	toolCalls          map[string]*llm.ToolCall
	toolCallOrder      []string
	usage              *llm.Usage
	err                error
	accepted           bool
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
	var contentOutputIndex *int
	toolCalls := map[string]*llm.ToolCall{}
	var toolCallOrder []string
	var continuationItems []llm.ContinuationItem
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
			if contentOutputIndex == nil && chunk.ContentOutputIndex != nil {
				contentOutputIndex = llm.OutputPosition(*chunk.ContentOutputIndex)
			}
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
		seenWithoutPosition := make(map[string]struct{}, len(chunk.ToolCalls))
		for _, tc := range chunk.ToolCalls {
			key := toolCallCollectionKey(tc)
			if tc.OutputIndex == nil {
				var positionedKey string
				positionedMatches := 0
				for _, candidateKey := range toolCallOrder {
					candidate := toolCalls[candidateKey]
					if candidate.OutputIndex != nil && candidate.ID == tc.ID {
						positionedKey = candidateKey
						positionedMatches++
					}
				}
				if positionedMatches > 1 {
					streamErr = fmt.Errorf("%w: provider omitted the output position for reused tool call ID %q", ErrAmbiguousToolCalls, tc.ID)
					return false
				}
				if positionedMatches == 1 {
					key = positionedKey
				}
			}
			if tc.OutputIndex == nil {
				if _, duplicate := seenWithoutPosition[tc.ID]; duplicate {
					streamErr = fmt.Errorf("%w: provider reused tool call ID %q without output positions", ErrAmbiguousToolCalls, tc.ID)
					return false
				}
				seenWithoutPosition[tc.ID] = struct{}{}
			}
			existing, ok := toolCalls[key]
			if !ok && tc.OutputIndex != nil {
				wireKey := "id:" + tc.ID
				if pending := toolCalls[wireKey]; pending != nil && pending.OutputIndex == nil {
					existing, ok = pending, true
					delete(toolCalls, wireKey)
					toolCalls[key] = existing
					for i := range toolCallOrder {
						if toolCallOrder[i] == wireKey {
							toolCallOrder[i] = key
							break
						}
					}
				}
			}
			if ok && existing.ID != tc.ID {
				streamErr = fmt.Errorf("%w: output position identifies both %q and %q", ErrAmbiguousToolCalls, existing.ID, tc.ID)
				return false
			}
			if !ok {
				id := strings.Clone(tc.ID)
				existing = &llm.ToolCall{ID: id, Type: strings.Clone(tc.Type)}
				toolCalls[key] = existing
				toolCallOrder = append(toolCallOrder, key)
				if tc.OutputIndex != nil {
					existing.OutputIndex = llm.OutputPosition(*tc.OutputIndex)
				}
			}
			if tc.Function.Name != "" {
				if existing.Function.Name != "" && existing.Function.Name != tc.Function.Name {
					streamErr = fmt.Errorf(
						"%w: tool call ID %q changed function name from %q to %q",
						ErrAmbiguousToolCalls,
						tc.ID,
						existing.Function.Name,
						tc.Function.Name,
					)
					return false
				}
				existing.Function.Name = strings.Clone(tc.Function.Name)
			}
			if existing.OutputIndex == nil && tc.OutputIndex != nil {
				existing.OutputIndex = llm.OutputPosition(*tc.OutputIndex)
			}
			if tc.Function.Arguments != "" {
				arguments := existing.Function.Arguments + strings.Clone(tc.Function.Arguments)
				if containsMultipleJSONValues(arguments) {
					streamErr = fmt.Errorf("%w: provider emitted multiple complete calls for tool call ID %q", ErrAmbiguousToolCalls, tc.ID)
					return false
				}
				existing.Function.Arguments = arguments
			}
		}
		for _, item := range chunk.CompletedItems {
			continuationItems = append(continuationItems, item.Clone())
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
		content:            contentBuilder.String(),
		thinking:           thinkingBuilder.String(),
		contentOutputIndex: contentOutputIndex,
		continuationItems:  continuationItems,
		toolCalls:          toolCalls,
		toolCallOrder:      toolCallOrder,
		usage:              iterUsage,
		err:                streamErr,
		accepted:           accepted,
	}
}

func toolCallCollectionKey(call llm.ToolCall) string {
	if call.OutputIndex != nil {
		return fmt.Sprintf("output:%d", *call.OutputIndex)
	}
	return "id:" + call.ID
}

func containsMultipleJSONValues(arguments string) bool {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	return decoder.Decode(&value) == nil
}
