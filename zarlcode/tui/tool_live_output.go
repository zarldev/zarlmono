package tui

import (
	"strconv"
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
	lines := renderPlain(width, text, withBodyPrefix("    "))
	if len(lines) <= toolResultMaxLines {
		return lines
	}
	hidden := len(lines) - toolResultMaxLines
	notice := renderPlain(width, "["+strconv.Itoa(hidden)+" earlier preview rows hidden]", withBodyPrefix("    "), withStyle(palette.Muted.On))
	return append(notice, lines[hidden:]...)
}
