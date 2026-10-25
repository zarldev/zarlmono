package tui_test

import (
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCompactionUsageDoesNotBorrowRootPrice(t *testing.T) {
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "root-model")
	ui.SetProvider("root-provider")
	ui.SetPricing(1, 2)
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root", Provider: "root-provider", Model: "root-model"})
	step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: "root", Inference: compact.Inference{Provider: "other", Model: "small", Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10}, Duration: time.Second}}})
	step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 10}})
	diagnostics := ui.CacheDiagnostics()
	if len(diagnostics) != 2 {
		t.Fatalf("targets=%+v", diagnostics)
	}
	other := diagnostics[0]
	if other.Provider != "other" || other.KnownCostUSD != 0 || other.BaseRateUnknownTasks != 1 || other.CompactorCalls != 1 || other.ChildTasks != 0 || other.CompactorDurationNanos != int64(time.Second) {
		t.Fatalf("compactor target=%+v", other)
	}
	if diagnostics[1].PromptTokens != 10 {
		t.Fatalf("root usage inflated: %+v", diagnostics[1])
	}
	// Manual compaction has no active task but still settles its reported usage.
	step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: "manual-compact", Inference: compact.Inference{Provider: "other", Model: "small", Usage: &llm.Usage{}}}})
	if got := ui.CacheDiagnostics()[0]; got.CompactorCalls != 2 || got.ReportedZeroUsageTasks != 1 {
		t.Fatalf("manual usage=%+v", got)
	}
}

func TestCompactionReasoningDiagnosticsPreservePartialCoverage(t *testing.T) {
	ui := tui.New()
	for _, usage := range []*llm.Usage{
		{CompletionTokens: 10, ReasoningTokens: 7, ReasoningTokensReported: true},
		{CompletionTokens: 2, ReasoningTokensReported: true},
		{CompletionTokens: 3},
		nil,
	} {
		step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{Inference: compact.Inference{Provider: "other", Model: "small", Usage: usage}}})
	}
	got := ui.CacheDiagnostics()[0]
	if got.CompactorCalls != 4 || got.ReasoningReportedSettlements != 2 || got.ReasoningTokens != 7 || got.CompletionTokens != 15 || got.MissingUsageTasks != 1 {
		t.Fatalf("diagnostics=%+v", got)
	}
	var restored tui.RunState
	restored.RestoreUsage(ui.UsageSnapshot())
	if saved := restored.UsageSnapshot().CacheTargets[0]; saved != got {
		t.Fatalf("restored diagnostics=%+v, want %+v", saved, got)
	}
}
