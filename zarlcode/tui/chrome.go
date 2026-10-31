package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/chrome"
)

// terminalChrome keeps decoration independent of theme selection. The existing
// renderer still owns output color-profile conversion and native image output.
func terminalChrome() chrome.Style {
	return chrome.Style{
		Theme: palette,
		ASCII: os.Getenv("ZARLCODE_ASCII") == "1",
		Plain: os.Getenv("NO_COLOR") != "",
	}
}

// listRow keeps selection independent of the pane's keyboard focus. Activity
// glyphs and metadata remain part of the caller's label, not a second cursor.
func listRow(style chrome.Style, left, right string, width int, selected bool) string {
	return style.Selection(rowLayout(style.Marker(selected, false)+left, right, width), width, selected)
}

// tabCluster uses the same selection surface as rows while retaining brackets
// as a non-color active-tab cue. Callers own clipping and the surrounding title.
func tabCluster(style chrome.Style, tabs []string, active int) string {
	parts := make([]string, len(tabs))
	for i, tab := range tabs {
		if i == active {
			label := "[ " + tab + " ]"
			parts[i] = style.Selection(label, ansi.StringWidth(label), true)
		} else {
			parts[i] = style.PaneLabel(tab, false)
		}
	}
	return strings.Join(parts, "  ")
}
