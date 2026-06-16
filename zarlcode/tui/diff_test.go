package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimeline_ShowsDiff(t *testing.T) {
	out := drive(t,
		teasink.DiffMsg{Path: "parser.go", Diff: "@@ -1,2 +1,2 @@\n-old code\n+new code\n"},
	)
	// diffs collapse into a per-iteration edit-group summary by default.
	if !strings.Contains(out, "edits (1 file)") {
		t.Errorf("edit group summary missing:\n%s", out)
	}
}
