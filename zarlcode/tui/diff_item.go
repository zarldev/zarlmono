package tui

import (
	"strconv"
	"strings"
)

// maxDiffLines caps how much of a diff the inline timeline item shows;
// the full diff lands in a detail pane in a later phase.
const maxDiffLines = 24

// diffItem renders a recorded file diff (unified) with +/- coloring. The diff
// content is immutable once recorded; only its collapsed state changes (via the
// per-row [+]/[-]), so a toggle bumps the version to refresh the inline render.
type diffItem struct {
	versioned
	path     string
	diff     string
	expanded bool // diff body shown ([-]) vs hidden ([+])
}

func (d *diffItem) finished() bool { return true }

func (d *diffItem) render(width int) []string {
	glyph := "[-] "
	if !d.expanded {
		glyph = "[+] "
	}
	head := palette.Subtle.On(glyph) + "± " + d.path
	if add, del := d.counts(); add+del > 0 {
		head += "  " + palette.Success.On("+"+strconv.Itoa(add)) + " " + palette.Error.On("-"+strconv.Itoa(del))
	}
	lines := []string{head}
	if !d.expanded {
		return lines
	}
	lines = append(lines, renderContentBlock(width, contentBlock{kind: contentDiff, text: d.diff, bodyPrefix: "  ", maxLines: maxDiffLines})...)
	return lines
}

// counts tallies added / removed lines for the row's summary badge.
func (d *diffItem) counts() (int, int) {
	return diffLineCounts(d.diff)
}

func diffLineCounts(diff string) (int, int) {
	add, del := 0, 0
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++"):
			add++
		case strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---"):
			del++
		}
	}
	return add, del
}

// toggle flips the diff's body drawer (clicked through its parent edit group).
func (d *diffItem) toggle() { d.expanded = !d.expanded; d.bump() }

func (tl *timeline) addDiff(path, diff string) {
	// Collapsed by default — the edits group reads as a list of changed paths;
	// the per-row [+] expands a diff on demand.
	tl.ensureEditGroup(0).add(&diffItem{path: path, diff: diff})
}

// diffLineColorizer returns the colour func for a unified-diff line, keyed on
// its prefix: additions Success (green), removals Error (red), hunk/file
// headers Info, context lines Muted so the changes pop. Returned as a bound
// method value so a wrapped line's continuation segments can be painted with
// the same colour as the original line (classification is by the first
// segment, not the wrapped tail).
func diffLineColorizer(ln string) func(string) string {
	switch {
	case strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++"):
		return palette.Success.On
	case strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---"):
		return palette.Error.On
	case strings.HasPrefix(ln, "@@"):
		return palette.Info.On
	default:
		return palette.Muted.On // context — de-emphasised
	}
}

// colorizeDiffLine paints one whole unified-diff line by its prefix. Plain
// ANSI, no lipgloss — the draw path is ANSI-aware (ansi.Truncate) so the
// colours survive clipping.
func colorizeDiffLine(ln string) string { return diffLineColorizer(ln)(ln) }
