//go:build linux

package tui_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// This measures actual PTY key writes through Bubble Tea and terminal output,
// not direct calls to Update. The inherited terminal harness polls output every
// 10ms, so reported percentiles include that observation granularity.
func TestToolOutputFloodTerminalResponsiveness(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.Update(tea.WindowSizeMsg{Width: 120, Height: 42})
	body := strings.Repeat("building synthetic package\n", 2000)
	for i := range 4 {
		id := fmt.Sprintf("live-%d", i)
		f.ui.Update(teasink.ToolStartedMsg{TaskID: "perf", ExecutionID: id, ToolID: id, ToolName: "bash"})
		f.ui.Update(teasink.ToolOutputMsg{TaskID: "perf", ExecutionID: id, ToolID: id, Text: body})
	}
	for i := range 100 {
		id := fmt.Sprintf("done-%d", i)
		f.ui.Update(teasink.ToolStartedMsg{TaskID: "perf", ExecutionID: id, ToolID: id, ToolName: "bash"})
		f.ui.Update(teasink.ToolCompletedMsg{TaskID: "perf", ExecutionID: id, ToolID: id, ToolName: "bash", FormattedResult: "completed synthetic output"})
	}
	expandFirstTool(f.ui)
	// Return focus to the composer while leaving the live drawer expanded.
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	terminal := startRewindTerminal(t, f)
	terminal.waitText(t, "▏")
	ctx, cancel := context.WithCancel(t.Context())
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n++
				for i := range 4 {
					id := fmt.Sprintf("live-%d", i)
					f.sink.OnToolOutput(ctx, runner.ToolOutputProgress{TaskID: "perf", ExecutionID: id, ToolID: id, ToolName: "bash", Text: fmt.Sprintf("progress %d\n", n) + body})
				}
				f.sink.Flush()
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-producerDone })
	samples := make([]time.Duration, 0, 30)
	for i := range 30 {
		// One unique glyph is observable in the terminal byte stream even when
		// tool redraws occur between key events. Multi-key markers can be split
		// across frames and falsely look absent without a terminal emulator.
		marker := string(rune(0x4e00 + i))
		terminal.output.clear()
		started := time.Now()
		terminal.keys(t, marker)
		terminal.waitText(t, marker)
		samples = append(samples, time.Since(started))
	}
	cancel()
	<-producerDone
	drainStarted := time.Now()
	f.sink.Drain()
	drainLatency := time.Since(drainStarted)
	terminal.stop(t)
	slices.Sort(samples)
	t.Logf("PTY input-to-output under four noisy tools: p50=%s p95=%s p99=%s drain=%s (30 samples; 10ms observation polling)", samples[15], samples[28], samples[29], drainLatency)
}
