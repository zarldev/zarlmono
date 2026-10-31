package tui_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func BenchmarkCompletedToolBody(b *testing.B) {
	for _, sample := range []struct{ name, tool, text string }{
		{"shell", "bash", strings.Repeat("compiled synthetic package successfully\n", 8000)},
		{"code", "read", strings.Repeat("value := computeResult() // synthetic code\n", 8000)},
		{"json", "inspect", `{"items":[` + strings.Repeat(`{"path":"synthetic.go","size":123},`, 7999) + `{"path":"last.go","size":456}]}`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ui := tui.New()
				ui.Update(tea.WindowSizeMsg{Width: 120, Height: 48})
				ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: sample.tool, Parameters: map[string]any{"path": "synthetic.go"}})
				ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: "call", ToolID: "call", ToolName: sample.tool, FormattedResult: sample.text})
				expandFirstTool(ui)
				_ = ui.View()
			}
		})
	}
}

func BenchmarkToolGroupUpdate(b *testing.B) {
	for _, siblings := range []int{10, 1000} {
		b.Run(strconv.Itoa(siblings), func(b *testing.B) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 120, Height: 48})
			for i := range siblings {
				id := strconv.Itoa(i)
				ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: id, ToolID: id, ToolName: "bash", Parameters: map[string]any{"command": "synthetic"}})
				ui.Update(teasink.ToolCompletedMsg{TaskID: "turn", ExecutionID: id, ToolID: id, ToolName: "bash", FormattedResult: strings.Repeat("completed synthetic output\n", 100)})
			}
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "live", ToolID: "live", ToolName: "bash"})
			expandFirstTool(ui)
			_ = ui.View()
			b.ReportAllocs()
			n := 0
			for b.Loop() {
				n++
				ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "live", ToolID: "live", Text: fmt.Sprintf("progress %d", n)})
				_ = ui.View()
			}
		})
	}
}
