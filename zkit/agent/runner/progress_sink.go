package runner

import (
	"fmt"
	"io"
	"os"
)

// ToolProgressSink writes one terse line per tool event to w. It embeds
// NopSink so it ignores content, conversation, steer, and compaction
// events — only tool start/complete/fail are surfaced.
//
//	runner.StderrSink                     // pre-built default
//	runner.ToolProgressSink{W: buf}       // custom writer
//
// Embed ToolProgressSink in a custom sink to get tool progress for free
// while overriding other event methods.
type ToolProgressSink struct {
	NopSink
	W io.Writer
}

// StderrSink is a ToolProgressSink that writes tool progress to os.Stderr.
// It is the default EventSink for a Runner constructed without WithSink.
var StderrSink = ToolProgressSink{W: os.Stderr}

// StdoutSink is a ToolProgressSink that writes tool progress to os.Stdout.
var StdoutSink = ToolProgressSink{W: os.Stdout}

// OnToolStarted writes "→ <tool name>" to W.
func (s ToolProgressSink) OnToolStarted(e ToolStarted) {
	fmt.Fprintf(s.W, "  → %s\n", e.ToolName)
}

// OnToolCompleted writes "✓ <tool name>" to W.
func (s ToolProgressSink) OnToolCompleted(e ToolCompleted) {
	fmt.Fprintf(s.W, "  ✓ %s\n", e.ToolName)
}

// OnToolFailed writes "✗ <tool name>: <error>" to W.
func (s ToolProgressSink) OnToolFailed(e ToolFailed) {
	fmt.Fprintf(s.W, "  ✗ %s: %s\n", e.ToolName, e.Error)
}
