package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// presentLiveToolOutput is the last display/storage boundary. Producers publish
// safe previews, but stored or third-party values must not regain terminal power.
func presentLiveToolOutput(text string) string {
	if text == "" {
		return ""
	}
	text = escapeToolInputControls(redactToolInputText(tools.RedactSecrets(text)))
	if len(text) <= tools.MaxToolOutputPreviewBytes {
		return text
	}
	const notice = "\n[live preview truncated for display]\n"
	end := tools.MaxToolOutputPreviewBytes - len(notice)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + notice
}

func (tl *timeline) updateToolOutput(executionID, toolID, text string) {
	if executionID == "" {
		return
	}
	ref, ok := tl.toolRef(toolEventKey(executionID, toolID))
	if !ok || ref.tool.state != toolRunning {
		return
	}
	safe := presentLiveToolOutput(text)
	if ref.tool.liveOutput == safe {
		return
	}
	change := tl.applyTranscript(transcript.ToolOutputUpdated{ExecutionID: executionID, ToolID: toolID, Output: safe})
	if !change.Changed() {
		return
	}
	selection := tl.captureDisclosureSelection()
	ref.tool.liveOutput = safe
	tl.bumpToolOwner(ref)
	tl.restoreDisclosureSelection(selection)
}

func (t *toolItem) resultSectionTitle() string {
	if t.showsLivePreview() {
		if t.state == toolRunning {
			return "live output:"
		}
		return "output preview (bounded):"
	}
	return "result:"
}

func (t *toolItem) showsLivePreview() bool {
	return t.result == "" && t.data == nil && t.liveOutput != ""
}

// Live output follows the newest retained lines so an expanded long-running
// call continues showing progress. Final results keep their existing renderer.
func renderLiveToolPreview(width int, text string) []string {
	// Every source line occupies at least one display row. Only the last 40
	// source lines can contribute to the visible tail; measure earlier lines
	// without allocating their padding, prefixes, and discarded rendered rows.
	start := len(text)
	for range toolResultMaxLines {
		index := strings.LastIndexByte(text[:start], '\n')
		if index < 0 {
			start = 0
			break
		}
		start = index
	}
	hidden := 0
	if start > 0 {
		bodyWidth := max(1, width-4)
		for line := range strings.SplitSeq(text[:start], "\n") {
			if previewLineFits(line, bodyWidth) {
				hidden++
			} else {
				hidden += len(wrapText(line, bodyWidth))
			}
		}
		start++ // Skip the delimiter, not the first visible source line.
	}
	lines := renderPlain(width, text[start:], withBodyPrefix("    "))
	if extra := len(lines) - toolResultMaxLines; extra > 0 {
		hidden += extra
		lines = lines[extra:]
	}
	if hidden == 0 {
		return lines
	}
	notice := renderPlain(width, "["+strconv.Itoa(hidden)+" earlier preview rows hidden]", withBodyPrefix("    "), withStyle(palette.Muted.On))
	return append(notice, lines...)
}

func previewLineFits(line string, width int) bool {
	if len(line) > width {
		return false
	}
	for i := range len(line) {
		if line[i] < ' ' || line[i] > '~' {
			return false // Tabs, Unicode, and controls retain the normal wrapper.
		}
	}
	return true
}
