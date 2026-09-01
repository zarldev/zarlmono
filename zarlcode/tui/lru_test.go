package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestLargeTranscriptRenderingRemainsStableAcrossCacheEviction(t *testing.T) {
	history := make([]llm.Message, 0, 300)
	for i := range 300 {
		history = append(history, llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("message-%03d unique content", i)})
	}
	ui := tui.New()
	ui.RestoreTranscript(history)
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	first := ansi.Strip(model.View().Content)
	second := ansi.Strip(model.View().Content)
	if first != second {
		t.Fatal("repeated transcript render changed after content cache overflow")
	}
	if !strings.Contains(second, "message-299 unique content") {
		t.Fatalf("latest transcript content missing after cache overflow:\n%s", second)
	}
}
