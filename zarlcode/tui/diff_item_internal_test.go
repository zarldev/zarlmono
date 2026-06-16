package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestColorizeDiffLine(t *testing.T) {
	UseTheme(theme.Theme{Success: "#00ff00", Error: "#ff0000", Info: "#00ffff"})
	defer UseTheme(theme.Theme{})

	if !strings.Contains(colorizeDiffLine("+added"), theme.Color("#00ff00").FG()) {
		t.Error("added line should use the Success colour")
	}
	if !strings.Contains(colorizeDiffLine("-removed"), theme.Color("#ff0000").FG()) {
		t.Error("removed line should use the Error colour")
	}
	if !strings.Contains(colorizeDiffLine("@@ -1 +1 @@"), theme.Color("#00ffff").FG()) {
		t.Error("hunk header should use the Info colour")
	}
	if c := colorizeDiffLine("+++ b/foo.go"); strings.Contains(c, theme.Color("#00ff00").FG()) {
		t.Error("file header (+++) must not be colored as an addition")
	}
	if c := colorizeDiffLine(" context"); c != " context" {
		t.Errorf("context line should be unchanged, got %q", c)
	}
}

func TestDiffItem_ExpandedView(t *testing.T) {
	d := &diffItem{
		path:     "foo.go",
		diff:     "@@ foo.go @@\n keep\n-old line\n+new line\n+another add",
		expanded: true,
	}
	out := d.render(80)
	joined := strings.Join(out, "\n")

	// Row header carries the path + a +adds/-dels badge.
	if !strings.Contains(out[0], "± foo.go") {
		t.Errorf("header should show the path, got %q", out[0])
	}
	if !strings.Contains(out[0], "+2") || !strings.Contains(out[0], "-1") {
		t.Errorf("header should show +2 -1 counts, got %q", out[0])
	}
	// The redundant "@@ foo.go @@" file header is dropped from the body.
	if strings.Contains(joined, "@@ foo.go @@") {
		t.Errorf("expanded body should drop the @@ path @@ header:\n%s", joined)
	}
	for _, want := range []string{"keep", "-old line", "+new line", "+another add"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expanded body missing %q:\n%s", want, joined)
		}
	}

	// Collapsed: just the header row, no body.
	d.expanded = false
	if got := d.render(80); len(got) != 1 {
		t.Errorf("collapsed diff should render one line, got %d:\n%s", len(got), strings.Join(got, "\n"))
	}
}
