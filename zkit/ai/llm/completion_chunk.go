package llm

import "strings"

// Clone returns an owned deep copy of c suitable for retention after a stream
// yield callback returns. It clones all string and slice backing storage,
// including every field of every ToolCall.
func (c CompletionChunk) Clone() CompletionChunk {
	clone := c
	clone.Content = strings.Clone(c.Content)
	clone.Thinking = strings.Clone(c.Thinking)
	clone.ContentOutputIndex = cloneInt(c.ContentOutputIndex)
	if c.ToolCalls != nil {
		clone.ToolCalls = make([]ToolCall, len(c.ToolCalls))
		for i, call := range c.ToolCalls {
			clone.ToolCalls[i] = call.Clone()
		}
	}
	if c.CompletedItems != nil {
		clone.CompletedItems = make([]ContinuationItem, len(c.CompletedItems))
		for i, item := range c.CompletedItems {
			clone.CompletedItems[i] = item.Clone()
		}
	}
	return clone
}
