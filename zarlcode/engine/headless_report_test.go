package engine_test

import (
	"encoding/json"
	"strings"
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

func TestReportSeparatesTotalsUnknownUsageAndResidual(t *testing.T) {
	t.Parallel()
	res := runner.TaskResult{
		Duration:   10 * time.Second,
		LastUsage:  &llm.Usage{PromptTokens: 5},
		TotalUsage: &llm.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 17},
		Timing:     runner.TaskTiming{ProviderAttempts: 3, AttemptsWithUsage: 2, ProviderDuration: 4 * time.Second, CallbackDuration: time.Second, RequestPreparationDuration: time.Second, ToolDispatchDuration: 2 * time.Second},
	}
	got := engine.Report(res)
	if got.PromptTokens != 5 || got.TotalUsage.PromptTokens != 12 || got.TotalUsage.CompletionTokens != 3 || got.TotalUsage.TotalTokens != 17 || got.UnattributedDuration != 3*time.Second || got.Timing.ProviderAttempts != 3 {
		t.Fatalf("report = %+v", got)
	}
	for _, usage := range []*llm.Usage{nil, {}} {
		got := engine.Report(runner.TaskResult{TotalUsage: usage})
		if (got.TotalUsage == nil) != (usage == nil) {
			t.Fatal("lost usage presence")
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"total_usage":null`) != (usage == nil) {
			t.Fatalf("json = %s", data)
		}
	}
}
