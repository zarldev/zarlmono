package llm

import "strings"

// Clone returns an owned deep copy of c suitable for retention after a stream
// yield callback returns. It clones all string and slice backing storage,
// including every field of every ToolCall.
func (c CompletionChunk) Clone() CompletionChunk {
	clone := c
	clone.Content = strings.Clone(c.Content)
	clone.Thinking = strings.Clone(c.Thinking)
	if c.ToolCalls != nil {
		clone.ToolCalls = make([]ToolCall, len(c.ToolCalls))
		for i, call := range c.ToolCalls {
			clone.ToolCalls[i] = ToolCall{
				ID:   strings.Clone(call.ID),
				Type: strings.Clone(call.Type),
				Function: ToolCallFunction{
					Name:      strings.Clone(call.Function.Name),
					Arguments: strings.Clone(call.Function.Arguments),
				},
			}
		}
	}
	return clone
}
