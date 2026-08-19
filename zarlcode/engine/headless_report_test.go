package engine_test

import (
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestReportUsesExactRequestAccounting(t *testing.T) {
	t.Parallel()

	res := runner.TaskResult{
		Reason:       runner.TerminalError,
		Cause:        runner.TerminalCauseStreamIdle,
		Iterations:   1,
		Duration:     2 * time.Second,
		SystemPrompt: "one two three",
		LastUsage: &llm.Usage{
			PromptTokens:     120,
			CachedTokens:     80,
			CompletionTokens: 7,
		},
		ToolSurface: runner.ToolSurface{
			Count:       4,
			JSONBytes:   2048,
			Fingerprint: "abc123",
		},
	}
	got := engine.Report(res)
	if got.PromptBytes != len(res.SystemPrompt) || got.PromptWords != 3 {
		t.Errorf("prompt accounting = %d bytes/%d words", got.PromptBytes, got.PromptWords)
	}
	if got.ToolCount != 4 || got.ToolJSONBytes != 2048 || got.ToolFingerprint != "abc123" {
		t.Errorf("tool accounting = %+v", got)
	}
	if got.PromptTokens != 120 || got.CachedTokens != 80 || got.CompletionTokens != 7 {
		t.Errorf("usage accounting = %+v", got)
	}
	if got.TerminalCause != runner.TerminalCauseStreamIdle {
		t.Errorf("terminal cause = %q", got.TerminalCause)
	}
}
