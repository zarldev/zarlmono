package runner

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type toolOutputPublisher struct {
	mu                sync.Mutex
	r                 *Runner
	spec              TaskSpec
	executionID       string
	toolID            string
	toolName          string
	parentExecutionID string
	parentToolID      string
	sequence          int
	sealed            bool
}

func newToolOutputPublisher(r *Runner, spec TaskSpec, call tools.ToolCall, parentExecutionID, parentToolID string, sequence int) *toolOutputPublisher {
	return &toolOutputPublisher{
		r: r, spec: spec, executionID: call.ExecutionID,
		toolID: call.ID.String(), toolName: call.ToolName.String(),
		parentExecutionID: parentExecutionID, parentToolID: parentToolID, sequence: sequence,
	}
}

func (p *toolOutputPublisher) report(ctx context.Context, update tools.ToolOutputUpdate) {
	text := boundedValidPreview(update.Text)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sealed {
		return
	}
	p.r.sink.OnToolOutput(ctx, ToolOutputProgress{
		TaskID: p.spec.ID, Depth: p.spec.Depth,
		ExecutionID: p.executionID, ToolID: p.toolID, ToolName: p.toolName,
		ParentExecutionID: p.parentExecutionID, ParentToolID: p.parentToolID,
		Sequence: p.sequence, Text: text,
	})
}

func (p *toolOutputPublisher) seal() {
	p.mu.Lock()
	p.sealed = true
	p.mu.Unlock()
}

func boundedValidPreview(text string) string {
	if !utf8.ValidString(text) {
		if len(text) > tools.MaxToolOutputPreviewBytes {
			text = text[:tools.MaxToolOutputPreviewBytes]
		}
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	if len(text) <= tools.MaxToolOutputPreviewBytes {
		return text
	}
	text = text[:tools.MaxToolOutputPreviewBytes]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
