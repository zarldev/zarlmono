package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTranscriptReader_MouseWheelCanReachConversationStart(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	for i := range 40 {
		taskID := fmt.Sprintf("turn-%d", i)
		model, _ = model.Update(teasink.ConversationStartedMsg{
			TaskID: taskID,
			Prompt: fmt.Sprintf("prompt %02d", i),
		})
		model, _ = model.Update(teasink.ContentMsg{
			TaskID: taskID,
			Delta:  fmt.Sprintf("answer %02d", i),
		})
	}

	model, _ = model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'r'})
	initial := ansi.Strip(model.View().Content)
	if !strings.Contains(initial, "prompt 39") {
		t.Fatalf("transcript reader did not open at the conversation tail:\n%s", initial)
	}
	if strings.Contains(initial, "prompt 00") {
		t.Fatalf("transcript reader opened at the conversation start:\n%s", initial)
	}

	for range 100 {
		model, _ = model.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	}
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "prompt 00") {
		t.Fatalf("mouse-wheel scrollback did not reach the conversation start:\n%s", out)
	}
}

func TestTranscriptReader_RestoreResizeAndScrollKeepsConversationContext(t *testing.T) {
	ui := tui.New()
	history := make([]llm.Message, 0, 120)
	for i := range 40 {
		history = append(history,
			llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("restored prompt %02d", i)},
			llm.Message{Role: llm.RoleAssistant, ReasoningContent: fmt.Sprintf("reasoning %02d before tool", i)},
			llm.Message{Role: llm.RoleAssistant, ReasoningContent: fmt.Sprintf("reasoning %02d after tool", i), Content: fmt.Sprintf("restored answer %02d", i)},
		)
	}
	ui.AddTranscriptMessages(history)
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	model, _ = model.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'r'})
	_ = model.View()

	for range 20 {
		model, _ = model.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	}
	model, _ = model.Update(tea.WindowSizeMsg{Width: 55, Height: 20})
	_ = model.View()
	for range 200 {
		model, _ = model.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	}
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "restored prompt 00") {
		t.Fatalf("restored transcript lost earliest conversation context after resize and scroll:\n%s", out)
	}
}
