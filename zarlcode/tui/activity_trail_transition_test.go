package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestActivityTrailCompletedCallDoesNotChangeMembership(t *testing.T) {
	ui := activityFixture(t)
	activityCall(ui, 0, "running")
	for i := 1; i < 9; i++ {
		activityCall(ui, i, "ok")
	}
	clickToolLabel(t, ui, "[+] tools")
	ui.Update(teasink.ToolCompletedMsg{TaskID: "activity", ExecutionID: "call-00", ToolID: "call-00", ToolName: "read", FormattedResult: "EVIDENCE_00"})
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "TARGET_00") || !strings.Contains(view, "TARGET_08") {
		t.Fatal("completion changed expanded group membership")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(ui.View().Content), "EVIDENCE_00") {
		t.Fatal("keyboard disclosure did not open first call")
	}
	clickToolLabel(t, ui, "[-] tools")
	activityCall(ui, 9, "running")
	if strings.Contains(ansi.Strip(ui.View().Content), "TARGET_") {
		t.Fatal("new call leaked outside collapsed tools")
	}
	clickToolLabel(t, ui, "[+] tools")
	if !strings.Contains(ansi.Strip(ui.View().Content), "TARGET_09") {
		t.Fatal("new call missing on expansion")
	}
}

func TestActivityTrailGroupCollapseHidesNativeImages(t *testing.T) {
	ui, output := expandedNativeScreenshot(t, 1)
	ui.Update(tea.WindowSizeMsg{Width: 70, Height: 50})
	for i := range 8 {
		activityCall(ui, i, "ok")
	}
	frame := output("\x1b[2J" + ui.View().Content)
	if !strings.Contains(frame, "\x1b_Ga=T,f=100") {
		t.Fatal("expanded group lost older image")
	}
	clickToolLabel(t, ui, "[-] tools")
	frame = output(ui.View().Content)
	if strings.Contains(frame, "\x1b_Ga=T,f=100") {
		t.Fatal("collapsed group leaked native image")
	}
	clickToolLabel(t, ui, "[+] tools")
	frame = output(ui.View().Content)
	if !strings.Contains(frame, "\x1b_Ga=T,f=100") {
		t.Fatal("reopening group lost image disclosure state")
	}
	ui.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	if frame = output(ui.View().Content); !strings.Contains(frame, "\x1b_Ga=T,f=100") {
		t.Fatal("resize lost image placement")
	}
}
