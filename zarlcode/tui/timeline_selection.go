package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type transcriptSelection struct {
	active bool
	anchor int // absolute rendered transcript line
	head   int // absolute rendered transcript line
}

func (tl *timeline) selectionActive() bool { return tl.selection.active }

func (tl *timeline) startSelection() {
	if len(tl.items) == 0 {
		return
	}
	tl.enterBrowse()
	line := tl.selectedAbsLine()
	total := tl.totalLines(tl.lwidth())
	if total == 0 {
		return
	}
	line = tl.nearestCopyableLine(clampNonNegative(line, total-1), total)
	tl.selection = transcriptSelection{active: true, anchor: line, head: line}
	tl.keepSelectionHeadVisible(total)
}

func (tl *timeline) nearestCopyableLine(line, total int) int {
	starts, _, _ := tl.layoutIndex(tl.lwidth())
	copyable := func(abs int) bool {
		for index, it := range tl.items {
			local := abs - starts[index]
			if local < 0 {
				return false
			}
			lines := tl.renderItem(it, tl.lwidth())
			if local < len(lines) {
				return strings.TrimSpace(cleanTranscriptCopyLine(it, lines[local])) != ""
			}
		}
		return false
	}
	for offset := range total {
		if candidate := line + offset; candidate < total && copyable(candidate) {
			return candidate
		}
		if offset > 0 {
			if candidate := line - offset; candidate >= 0 && copyable(candidate) {
				return candidate
			}
		}
	}
	return line
}

func (tl *timeline) cancelSelection() { tl.selection = transcriptSelection{} }

func (tl *timeline) moveSelection(delta int) {
	if !tl.selection.active {
		return
	}
	tl.moveSelectionTo(tl.selection.head + delta)
}

func (tl *timeline) moveSelectionTo(line int) {
	if !tl.selection.active {
		return
	}
	total := tl.totalLines(tl.lwidth())
	if total == 0 {
		tl.cancelSelection()
		return
	}
	tl.selection.head = clampNonNegative(line, total-1)
	tl.keepSelectionHeadVisible(total)
}

func (tl *timeline) moveSelectionTop() { tl.moveSelectionTo(0) }

func (tl *timeline) moveSelectionBottom() {
	total := tl.totalLines(tl.lwidth())
	if total == 0 {
		tl.cancelSelection()
		return
	}
	tl.moveSelectionTo(total - 1)
}

func (tl *timeline) selectedText() string {
	if !tl.selection.active {
		return ""
	}
	width := tl.lwidth()
	lo, hi := tl.selectionRange()
	if hi < lo {
		return ""
	}
	starts, _, total := tl.layoutIndex(width)
	if total == 0 {
		return ""
	}
	lo = clampNonNegative(lo, total-1)
	hi = clampNonNegative(hi, total-1)

	lines := make([]string, 0, hi-lo+1)
	for i, it := range tl.items {
		if i > 0 && !itemNested(it) {
			sep := starts[i] - 1
			if sep >= lo && sep <= hi {
				lines = append(lines, "")
			}
		}
		itemLines := tl.renderItem(it, width)
		for k, line := range itemLines {
			abs := starts[i] + k
			if abs < lo {
				continue
			}
			if abs > hi {
				break
			}
			lines = append(lines, cleanTranscriptCopyLine(it, line))
		}
	}
	return strings.Join(trimBlankEdges(lines), "\n")
}

func (tl *timeline) finishSelection() string {
	text := tl.selectedText()
	tl.cancelSelection()
	return text
}

func (tl *timeline) selectionRange() (int, int) {
	lo, hi := tl.selection.anchor, tl.selection.head
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

func (tl *timeline) selectedAbsLine() int {
	if len(tl.items) == 0 {
		return 0
	}
	starts, _, total := tl.layoutIndex(tl.lwidth())
	if total == 0 {
		return 0
	}
	if tl.sel < 0 {
		tl.sel = 0
	}
	if tl.sel >= len(tl.items) {
		tl.sel = len(tl.items) - 1
	}
	tl.clampSelLocal(tl.lwidth())
	return clampNonNegative(starts[tl.sel]+tl.selLocal, total-1)
}

func (tl *timeline) keepSelectionHeadVisible(total int) {
	h := tl.viewHeight
	if h <= 0 {
		return
	}
	head := tl.selection.head
	if head < tl.scrollTop {
		tl.scrollTop = head
	} else if head >= tl.scrollTop+h {
		tl.scrollTop = head - h + 1
	}
	tl.clampScroll(total)
}

func (tl *timeline) totalLines(width int) int {
	_, _, total := tl.layoutIndex(width)
	return total
}

func cleanTranscriptCopyLine(it item, line string) string {
	plain := ansi.Strip(line)
	switch it.(type) {
	case *userItem, *queuedUserItem, *assistantItem:
		plain = stripTranscriptRail(plain)
	}
	return strings.TrimRight(plain, " \t")
}

func stripTranscriptRail(line string) string {
	for strings.HasPrefix(line, "⇢ ") {
		line = strings.TrimPrefix(line, "⇢ ")
	}
	line = strings.TrimPrefix(line, "◷ queued ")
	line = strings.TrimPrefix(line, "▌ ")
	line = strings.TrimPrefix(line, "│ ")
	if line == "└─" || line == "├─" {
		return ""
	}
	if strings.HasPrefix(line, "[you]") || strings.HasPrefix(line, "[zarl]") {
		return ""
	}
	return line
}

func trimBlankEdges(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func clampNonNegative(n, hi int) int {
	if n < 0 {
		return 0
	}
	if n > hi {
		return hi
	}
	return n
}

func transcriptItemText(it item) string {
	var text string
	switch value := it.(type) {
	case *userItem:
		text = value.text
	case *queuedUserItem:
		text = value.text
	case *assistantItem:
		text = value.content
	case *thinkingItem:
		text = value.text
	case *noticeItem:
		text = ansi.Strip(value.text)
	case *toolItem:
		text = transcriptToolText(value)
	case *diffItem:
		text = value.diff
	default:
		lines := it.render(maxTranscriptWidth)
		for index := range lines {
			lines[index] = cleanTranscriptCopyLine(it, lines[index])
		}
		text = strings.Join(lines, "\n")
	}
	return strings.TrimSpace(text)
}

func transcriptToolText(tool *toolItem) string {
	header := tool.name
	if tool.arg != "" {
		header += " " + tool.arg
	}
	if tool.result == "" {
		return header
	}
	return header + "\n\n" + tool.result
}
