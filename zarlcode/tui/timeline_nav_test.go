package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineNavigationPreservesTranscript(t *testing.T) {
	m := tui.New()
	step(t, m, window(100, 20))
	for _, s := range []string{"one", "two", "three"} {
		step(t, m, teasink.ContentMsg{TaskID: "t", Delta: s + "\n"})
	}
	step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	step(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "one") {
		t.Fatalf("navigation lost transcript:\n%s", out)
	}
}

func TestTimelineScrollbackRetainsTurnsAcrossIncrementalRenders(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	_ = model.View()

	for i := range 10 {
		taskID := fmt.Sprintf("turn-%d", i)
		model, _ = model.Update(teasink.ConversationStartedMsg{
			TaskID: taskID,
			Prompt: fmt.Sprintf("prompt %02d", i),
		})
		model, _ = model.Update(teasink.ContentMsg{
			TaskID: taskID,
			Delta:  fmt.Sprintf("answer %02d", i),
		})
		_ = model.View()
	}

	for range 100 {
		model, _ = model.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	}
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "prompt 00") {
		t.Fatalf("incremental rendering lost earlier turns from scrollback:\n%s", out)
	}
}
