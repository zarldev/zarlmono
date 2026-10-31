package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// BenchmarkToolOutputFlood measures fresh cumulative previews through Update and
// View, including expanded drawers and history rather than only cached frames.
func BenchmarkToolOutputFlood(b *testing.B) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, history := range []int{0, 10000} {
		for _, expanded := range []bool{false, true} {
			b.Run(fmt.Sprintf("history=%d/expanded=%t", history, expanded), func(b *testing.B) {
				ui := tui.New()
				ui.Update(tea.WindowSizeMsg{Width: 120, Height: 48})
				messages := make([]llm.Message, history)
				for i := range messages {
					messages[i] = llm.Message{Role: llm.RoleUser, Content: "Synthetic history"}
				}
				ui.AddTranscriptMessages(messages)
				ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", ToolName: "bash", Parameters: map[string]any{"command": "synthetic"}})
				if expanded {
					expandFirstTool(ui)
				}
				// Two different snapshots ensure every iteration invalidates the live row.
				body := strings.Repeat("building package: synthetic progress line\n", 1500)
				previews := [2]string{body + "latest progress A\n", body + "latest progress B\n"}
				ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: previews[0]})
				_ = ui.View()
				b.ReportAllocs()
				n := 0
				for b.Loop() {
					n++
					ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: previews[n%2]})
					_ = ui.View()
				}
			})
		}
	}
}
