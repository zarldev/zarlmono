package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineNestedProgramToolRows(t *testing.T) {
	out := drive(t, teasink.ToolStartedMsg{TaskID: "t", ToolID: "p", ToolName: "program"}, teasink.ToolStartedMsg{TaskID: "t", ToolID: "r", ParentToolID: "p", ToolName: "read", Parameters: map[string]any{"path": "main.go"}})
	if !strings.Contains(out, "tools (1)") {
		t.Fatalf("program group missing:\n%s", out)
	}
}
