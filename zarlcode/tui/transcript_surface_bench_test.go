package tui_test

import (
	"fmt"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// BenchmarkTranscriptSurfaceView includes the cell painter and screen flattening,
// not just cached item strings. History is warmed outside the measured loop.
func BenchmarkTranscriptSurfaceView(b *testing.B) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, history := range []int{100, 10000} {
		for _, browse := range []bool{false, true} {
			b.Run(fmt.Sprintf("history=%d/browse=%t", history, browse), func(b *testing.B) {
				ui := tui.New()
				ui.Update(tea.WindowSizeMsg{Width: 120, Height: 48})
				messages := make([]llm.Message, 0, history*2)
				for range history {
					messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "Inspect this synthetic change."}, llm.Message{Role: llm.RoleAssistant, Content: "**Verified.** A short response with a list.\n\n- one\n- two"})
				}
				ui.AddTranscriptMessages(messages)
				_ = ui.View()
				if browse {
					ui.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
					_ = ui.View()
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					_ = ui.View()
				}
			})
		}
	}
}

// BenchmarkTranscriptSurfaceUpdate measures warmed tool-output updates against a
// fixed-size cumulative preview, separating history cost from growing-body cost.
func BenchmarkTranscriptSurfaceUpdate(b *testing.B) {
	tui.UseTheme(theme.DarkDefault())
	defer tui.UseTheme(theme.Theme{})
	for _, history := range []int{100, 10000} {
		b.Run(strconv.Itoa(history), func(b *testing.B) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 120, Height: 48})
			messages := make([]llm.Message, history)
			for i := range messages {
				messages[i] = llm.Message{Role: llm.RoleUser, Content: "Synthetic history"}
			}
			ui.AddTranscriptMessages(messages)
			ui.Update(teasink.ConversationStartedMsg{TaskID: "bench", Prompt: "inspect"})
			ui.Update(teasink.ToolStartedMsg{TaskID: "bench", ExecutionID: "call", ToolID: "call", ToolName: "bash", Parameters: map[string]any{"command": "synthetic"}})
			_ = ui.View()
			b.ReportAllocs()
			b.ResetTimer()
			n := 0
			for b.Loop() {
				n++
				ui.Update(teasink.ToolOutputMsg{TaskID: "bench", ExecutionID: "call", ToolID: "call", ToolName: "bash", Text: fmt.Sprintf("synthetic output %08d", n)})
				_ = ui.View()
			}
		})
	}
}
