package tui_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestFailedToolDisplaysPartialOutputAndImage(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"partial output", "terminal failure"} {
		t.Run(raw, func(t *testing.T) {
			var model tea.Model = tui.New()
			for _, msg := range []tea.Msg{
				tea.WindowSizeMsg{Width: 100, Height: 50},
				teasink.ConversationStartedMsg{TaskID: "task"},
				teasink.ToolStartedMsg{TaskID: "task", ToolID: "call", ToolName: "capture"},
				teasink.ToolFailedMsg{TaskID: "task", ToolID: "call", ToolName: "capture", Error: "terminal failure", RawOutput: raw, Kind: tools.Kinds.TRANSIENT,
					Parts: []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngBytes.Bytes()), "image/png")}},
				tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyEnter},
				tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyEnter},
			} {
				model, _ = model.Update(msg)
			}
			rendered := ansi.Strip(model.View().Content)
			for _, want := range []string{"terminal failure", raw, "Image · 2 × 2", "[transient]"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("missing %q:\n%s", want, rendered)
				}
			}
			if strings.Count(rendered, "terminal failure") != 1 {
				t.Fatalf("duplicated error:\n%s", rendered)
			}
		})
	}
}
