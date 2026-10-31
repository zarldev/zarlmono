package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const largeToolPreviewBytes = 64 * 1024

// Large code/log previews deliberately omit syntax highlighting, not stored
// content. Redact before slicing: a cutoff must not expose a partial credential.
func renderLargeToolPreview(width int, text string, limit int) []string {
	text = escapeToolInputControls(redactToolInputText(text))
	end := min(len(text), largeToolPreviewBytes, max(1, width)*limit*utf8.UTFMax)
	for end < len(text) && end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	prefix := text[:end]
	start := 0
	for row := range limit {
		index := strings.IndexByte(prefix[start:], '\n')
		if index < 0 {
			break
		}
		start += index + 1
		if row == limit-1 {
			prefix = prefix[:start]
		}
	}
	lines := wrapText(prefix, width)
	if len(lines) >= limit {
		lines = lines[:limit-1]
	}
	notice := ansi.Truncate("… plain preview; full result retained", max(1, width), "…")
	return append(lines, palette.Muted.On(notice))
}
