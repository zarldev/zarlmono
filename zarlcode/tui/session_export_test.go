package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRestoredTranscriptShowsMultimodalUserContent(t *testing.T) {
	ui := tui.New()
	ui.RestoreTranscript([]llm.Message{{
		Role:    llm.RoleUser,
		Content: "compare these inputs",
		Parts: []llm.ContentPart{
			llm.TextPart("compare these inputs"),
			llm.ImagePartFromURL("https://example.com/assets/screenshot.png?size=large"),
			{Type: llm.ContentTypeAudio, Audio: &llm.AudioData{DataURI: "data:audio/wav;base64,c2VjcmV0", Format: "wav"}},
			llm.VideoPartFromDataURI("data:video/mp4;base64,c2VjcmV0", "video/mp4"),
		},
	}})
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := ansi.Strip(model.View().Content)

	for _, want := range []string{
		"compare these inputs",
		"[image: screenshot.png]",
		"[audio: wav]",
		"[video: video/mp4]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("restored transcript missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "compare these inputs") != 1 {
		t.Errorf("restored text part duplicated content:\n%s", out)
	}
	if strings.Contains(out, "c2VjcmV0") {
		t.Fatalf("restored transcript exposed encoded attachment data:\n%s", out)
	}
}

func TestMarkdownSessionExportCompletesWithoutBlockingTheUI(t *testing.T) {
	target := filepath.Join(t.TempDir(), "session.md")
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreHistory([]llm.Message{{Role: llm.RoleUser, Content: "export me"}})
	ui := tui.New()
	ui.SetLiveRunner(live)
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(tea.PasteMsg{Content: "/export " + target})
	model, exportCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export submission returned no command")
	}

	model, toastCmd := model.Update(exportCmd())
	if toastCmd == nil {
		t.Fatal("successful export returned no toast expiry command")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(body), "## Conversation") || !strings.Contains(string(body), "export me") {
		t.Fatalf("export missing conversation Markdown:\n%s", body)
	}
	if out := ansi.Strip(model.View().Content); !strings.Contains(out, "exported") {
		t.Fatalf("UI missing export confirmation:\n%s", out)
	}

	// Bubble Tea can dispatch the same command closure more than once. The first
	// invocation owns the timer; a duplicate must return instead of waiting on the
	// already-drained timer and wedging program shutdown.
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		toastCmd()
	}()
	time.Sleep(25 * time.Millisecond)

	duplicateDone := make(chan struct{})
	go func() {
		defer close(duplicateDone)
		toastCmd()
	}()
	select {
	case <-duplicateDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("duplicate export toast command blocked")
	}
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("export toast command did not terminate")
	}
}

func TestMarkdownSessionExportPreservesReasoningToolsAndAttachments(t *testing.T) {
	target := filepath.Join(t.TempDir(), "rich-session.md")
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreHistory([]llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "inspect this",
			Parts: []llm.ContentPart{
				llm.TextPart("inspect this"),
				llm.ImagePartFromURL("https://example.com/assets/screenshot.png"),
				{Type: llm.ContentTypeAudio, Audio: &llm.AudioData{DataURI: "data:audio/wav;base64,c2VjcmV0", Format: "wav"}},
			},
		},
		{
			Role:             llm.RoleAssistant,
			ReasoningContent: "reasoning that was previously omitted",
			ToolCalls: []llm.ToolCall{{
				ID: "call_1",
				Function: llm.ToolCallFunction{
					Name:      "read",
					Arguments: "{\n  \"path\": \"main.go\"\n}",
				},
			}},
		},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "package main"},
	})
	ui := tui.New()
	ui.SetLiveRunner(live)
	var model tea.Model = ui
	model, _ = model.Update(tea.PasteMsg{Content: "/export " + target})
	_, exportCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export submission returned no command")
	}
	if msg := exportCmd(); msg == nil {
		t.Fatal("export command returned no result")
	}

	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"inspect this",
		"Image attachment: `screenshot.png` — source `https://example.com/assets/screenshot.png`",
		"Audio attachment: `wav` — embedded data omitted",
		"#### Reasoning",
		"reasoning that was previously omitted",
		"#### Tool call: read",
		"- ID: `call_1`",
		"\"path\": \"main.go\"",
		"- Tool call ID: `call_1`",
		"package main",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rich export missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "inspect this") != 1 {
		t.Errorf("rich export duplicated matching text part:\n%s", text)
	}
	if strings.Contains(text, "c2VjcmV0") {
		t.Fatalf("rich export exposed encoded attachment payload:\n%s", text)
	}
}

func TestMarkdownSessionExportRejectsExistingExplicitPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "session.md")
	const original = "keep this file"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatalf("seed export target: %v", err)
	}
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreHistory([]llm.Message{{Role: llm.RoleUser, Content: "export me"}})
	ui := tui.New()
	ui.SetLiveRunner(live)
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(tea.PasteMsg{Content: "/export " + target})
	model, exportCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export submission returned no command")
	}

	model, _ = model.Update(exportCmd())
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "export: create export") || !strings.Contains(out, target) {
		t.Fatalf("existing explicit path did not produce a collision error:\n%s", out)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original target: %v", err)
	}
	if string(body) != original {
		t.Fatalf("explicit export overwrote existing target: %q", body)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(target), "session-*.md")); err != nil {
		t.Fatalf("glob alternate exports: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("explicit export silently wrote alternate path: %v", matches)
	}
}
