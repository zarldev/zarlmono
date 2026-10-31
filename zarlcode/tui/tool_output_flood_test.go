package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestLiveOutputFloodRetainsTailAndExactHiddenRows(t *testing.T) {
	for _, rows := range []int{1, 39, 40, 41, 1500} {
		for _, trailingNewline := range []bool{false, true} {
			t.Run(fmt.Sprintf("rows=%d/newline=%t", rows, trailingNewline), func(t *testing.T) {
				ui := liveOutputFixture()
				output := strings.Repeat("older progress\n", rows-1) + "NEWEST_PREVIEW_ROW"
				if trailingNewline {
					output += "\n"
				}
				ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: output})
				view := ansi.Strip(ui.View().Content)
				if !strings.Contains(view, "NEWEST_PREVIEW_ROW") {
					t.Fatal("lost newest row")
				}
				total := rows
				if trailingNewline {
					total++
				}
				if total > 40 {
					notice := fmt.Sprintf("[%d earlier preview rows hidden]", total-40)
					if !strings.Contains(view, notice) {
						t.Fatalf("missing %q", notice)
					}
				} else if strings.Contains(view, "earlier preview rows hidden") {
					t.Fatal("hid visible rows")
				}
				for _, entry := range ui.CanonicalThread().Entries() {
					if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL && entry.Payload.ToolOutputPreview != output {
						t.Fatal("display truncation changed stored preview")
					}
				}
			})
		}
	}
}

func TestLiveOutputFloodWrappingMatchesShortTail(t *testing.T) {
	// A hidden prefix must not change wrapping of the remaining paragraphs.
	for _, line := range []string{"\tindented progress", "界界 é progress", strings.Repeat("long-word-", 40), "", "  "} {
		t.Run(fmt.Sprintf("bytes=%d", len(line)), func(t *testing.T) {
			tail := strings.Repeat(line+"\n", 45) + "NEWEST_PREVIEW_ROW"
			long := liveOutputFixture()
			short := liveOutputFixture()
			long.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: strings.Repeat("hidden progress\n", 800) + tail})
			short.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: tail})
			for _, width := range []int{140, 90, 140} {
				long.Update(tea.WindowSizeMsg{Width: width, Height: 100})
				short.Update(tea.WindowSizeMsg{Width: width, Height: 100})
				// The only text difference is the count in the hidden-row notice.
				longView := ansi.Strip(long.View().Content)
				shortView := ansi.Strip(short.View().Content)
				longTail := previewTailRows(longView)
				shortTail := previewTailRows(shortView)
				if longTail == "" || longTail != shortTail {
					t.Fatal("hidden prefix changed wrapped tail or resize behavior")
				}
			}
		})
	}
}

func previewTailRows(view string) string {
	_, tail, ok := strings.Cut(view, "earlier preview rows hidden]")
	if !ok {
		return ""
	}
	// Skip notice padding (its digit count differs), then compare only the
	// preview rows rather than the status surface below them.
	_, tail, _ = strings.Cut(tail, "\n")
	tail, _, ok = strings.Cut(tail, "NEWEST_PREVIEW_ROW")
	if !ok {
		return ""
	}
	return tail
}

func TestLiveOutputFloodStillRedactsCredentials(t *testing.T) {
	for _, credential := range []string{
		"Authorization: Basic CANARY", "PROXY_AUTHORIZATION='Bearer CANARY'",
		"https://user:CANARY@example.test/path", "custom+scheme://user:CANARY@example.test/path",
		"PASSWORD=CANARY", "paſſword=CANARY", "api_Key=CANARY", "client-secret=CANARY",
		"secret=CANARY", "refresh_token=CANARY", "Bearer CANARY",
	} {
		t.Run(credential, func(t *testing.T) {
			ui := liveOutputFixture()
			output := strings.Repeat("building package: progress\n", 1000) + credential
			ui.Update(teasink.ToolOutputMsg{TaskID: "turn", ExecutionID: "execution", ToolID: "bash", Text: output})
			for _, entry := range ui.CanonicalThread().Entries() {
				if entry.Kind == transcript.EntryKinds.ENTRYTOOLCALL {
					if strings.Contains(entry.Payload.ToolOutputPreview, "CANARY") || !strings.Contains(entry.Payload.ToolOutputPreview, "[REDACTED]") {
						t.Fatal("credential crossed storage boundary")
					}
				}
			}
		})
	}
}
