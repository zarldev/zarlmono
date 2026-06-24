package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestSessionapplyConversationStartedRootOwnsRunAndPromptState(t *testing.T) {
	s := NewSession("~", t.TempDir(), "")
	s.SkipStartedPrompt = "queued prompt"
	s.Run.window = 1234

	effect := s.applyConversationStarted(teasink.ConversationStartedMsg{
		TaskID:           "task-1",
		Depth:            0,
		Prompt:           "queued prompt",
		ParentToolCallID: "parent-tool",
		AgentName:        "coder",
	}, time.Now())

	if effect.PromptToRender != "" {
		t.Fatalf("PromptToRender = %q, want skipped prompt suppressed", effect.PromptToRender)
	}
	if s.SkipStartedPrompt != "" {
		t.Fatalf("SkipStartedPrompt = %q, want consumed", s.SkipStartedPrompt)
	}
	if !s.Run.Running {
		t.Fatal("Run.Running = false, want true")
	}
	if s.Run.window != 1234 {
		t.Fatalf("Run.window = %d, want preserved across reset", s.Run.window)
	}
	if s.LastParentToolCallID != "parent-tool" || s.LastAgentName != "coder" {
		t.Fatalf("parent/agent not recorded: parent=%q agent=%q", s.LastParentToolCallID, s.LastAgentName)
	}
}

func TestSessionapplyConversationStartedReturnsNonSkippedPrompt(t *testing.T) {
	s := NewSession("~", t.TempDir(), "")
	s.SkipStartedPrompt = "queued prompt"

	effect := s.applyConversationStarted(teasink.ConversationStartedMsg{
		TaskID: "task-1",
		Depth:  0,
		Prompt: "fresh prompt",
	}, time.Now())

	if effect.PromptToRender != "fresh prompt" {
		t.Fatalf("PromptToRender = %q, want fresh prompt", effect.PromptToRender)
	}
	if s.SkipStartedPrompt != "" {
		t.Fatalf("SkipStartedPrompt = %q, want consumed", s.SkipStartedPrompt)
	}
}

func TestSessionApplyLoadSkillLifecycle(t *testing.T) {
	s := NewSession("~", t.TempDir(), "")

	s.applyToolStarted(teasink.ToolStartedMsg{
		TaskID:     "task-1",
		ToolID:     "tool-1",
		ToolName:   "load_skill",
		Parameters: map[string]any{"name": "go-testing"},
	})
	if got := s.PendingSkillNames["tool-1"]; got != "go-testing" {
		t.Fatalf("pending skill = %q, want go-testing", got)
	}
	if s.Run.toolsRunning != 1 {
		t.Fatalf("toolsRunning = %d, want 1", s.Run.toolsRunning)
	}

	effect := s.applyToolCompleted(teasink.ToolCompletedMsg{
		TaskID:   "task-1",
		ToolID:   "tool-1",
		ToolName: "load_skill",
		Result:   "loaded",
		Duration: time.Second,
	})
	if effect.LoadedSkillName != "go-testing" {
		t.Fatalf("LoadedSkillName = %q, want go-testing", effect.LoadedSkillName)
	}
	if _, ok := s.PendingSkillNames["tool-1"]; ok {
		t.Fatal("pending skill was not cleared")
	}
	if s.LastToolResult != "loaded" {
		t.Fatalf("LastToolResult = %#v, want loaded", s.LastToolResult)
	}
	if s.Run.toolsRunning != 0 {
		t.Fatalf("toolsRunning = %d, want 0", s.Run.toolsRunning)
	}
}

func TestSession_ConversationEndedErrorRootSetsToastAndStopsRun(t *testing.T) {
	s := NewSession("~", t.TempDir(), "")
	s.Run.Running = true

	effect := s.applyConversationEnded(teasink.ConversationEndedMsg{
		TaskID: "task-1",
		Depth:  0,
		Reason: runner.TerminalError,
		Error:  "boom",
	}, time.Now())

	if !effect.ToastChanged {
		t.Fatal("ToastChanged = false, want true")
	}
	if s.Run.Running {
		t.Fatal("Run.Running = true, want false")
	}
	if !strings.Contains(s.Toast, "boom") || !strings.HasPrefix(s.Toast, "✗") {
		t.Fatalf("Toast = %q, want error toast containing boom", s.Toast)
	}
}

func TestSession_ConversationEndedErrorSubagentDoesNotSetRootToast(t *testing.T) {
	s := NewSession("~", t.TempDir(), "")
	s.Run.Running = true

	effect := s.applyConversationEnded(teasink.ConversationEndedMsg{
		TaskID: "task-1",
		Depth:  1,
		Reason: runner.TerminalError,
		Error:  "child boom",
	}, time.Now())

	if effect.ToastChanged {
		t.Fatal("ToastChanged = true, want false for subagent failure")
	}
	if s.Toast != "" {
		t.Fatalf("Toast = %q, want no root toast for subagent failure", s.Toast)
	}
	if !s.Run.Running {
		t.Fatal("Run.Running = false, want parent run unchanged")
	}
}

func TestUserFacingProviderErrorParsesJSONBlob(t *testing.T) {
	raw := `stream: POST "https://api.example/v1/chat/completions": 400 Bad Request {"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 140000 tokens.","type":"invalid_request_error","code":"context_length_exceeded","param":"messages"}}`

	got := userFacingProviderError(raw)

	if strings.Contains(got, `{"error"`) || strings.Contains(got, "POST") {
		t.Fatalf("got raw provider blob: %q", got)
	}
	for _, want := range []string{"maximum context length", "invalid_request_error", "context_length_exceeded", "messages"} {
		if !strings.Contains(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestSession_ConversationEndedErrorAddsPersistentNotice(t *testing.T) {
	s := NewSession("~", t.TempDir(), "")
	raw := `completion: {"error":{"message":"token budget exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`

	effect := s.applyConversationEnded(teasink.ConversationEndedMsg{
		TaskID: "task-1",
		Depth:  0,
		Reason: runner.TerminalError,
		Error:  raw,
	}, time.Now())

	if effect.Notice == "" {
		t.Fatal("Notice empty, want persistent provider error notice")
	}
	if !strings.Contains(s.Toast, "token budget exceeded") || strings.Contains(s.Toast, `{"error"`) {
		t.Fatalf("Toast = %q, want parsed provider message", s.Toast)
	}
}
