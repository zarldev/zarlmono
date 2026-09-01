package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolEffectSummariesRenderInTimeline(t *testing.T) {
	tests := []struct {
		name    string
		effects []tools.Effect
		want    string
	}{
		{name: "rename", effects: []tools.Effect{renameEffect()}, want: "renamed old.go → new.go"},
		{name: "multiple files", effects: []tools.Effect{tools.NewFileEffect(tools.FileModify, "a.go"), tools.NewFileEffect(tools.FileCreate, "b.go")}, want: "changed 2 files"},
		{name: "process", effects: []tools.Effect{truncatedProcessEffect()}, want: "exit 1, output truncated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var model tea.Model = tui.New()
			model, _ = model.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			model, _ = model.Update(teasink.ToolStartedMsg{TaskID: "t", ToolID: "tool", ToolName: "edit"})
			model, _ = model.Update(teasink.ToolCompletedMsg{TaskID: "t", ToolID: "tool", ToolName: "edit", Effects: tt.effects, Duration: time.Millisecond})
			model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			out := ansi.Strip(model.View().Content)
			if !strings.Contains(out, tt.want) {
				t.Fatalf("expanded tool missing effect summary %q:\n%s", tt.want, out)
			}
		})
	}
}

func renameEffect() tools.Effect {
	e := tools.NewFileEffect(tools.FileRename, "new.go")
	e.File.FromPath = "old.go"
	return e
}

func truncatedProcessEffect() tools.Effect {
	e := tools.NewProcessEffect("go test ./...", 1)
	e.Process.OutputTruncated = true
	return e
}
