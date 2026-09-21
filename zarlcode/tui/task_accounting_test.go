package tui_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTerminalTaskUsageSettlesOnceWithTaskPricing(t *testing.T) {
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "parent-model")
	ui.SetProvider("openai")
	ui.SetPricing(1, 2)
	usage := &llm.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100}
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "openai", Model: "parent-model"})
	// Changes to active pricing must not reprice an already-started invocation.
	ui.SetPricing(20, 30)
	for _, event := range []teasink.ConversationStartedMsg{
		{TaskID: "child", Depth: 1, Provider: "other", Model: "child-model"},
		{TaskID: "grandchild", Depth: 2, Provider: "other", Model: "grandchild-model"},
	} {
		_, _ = ui.Update(event)
	}
	for _, event := range []teasink.ProviderAttemptSettledMsg{
		{TaskID: "root", Attempt: 1, Usage: usage},
		{TaskID: "root", Attempt: 1, Usage: usage},
		{TaskID: "child", Depth: 1, Attempt: 1, Usage: usage},
	} {
		_, _ = ui.Update(event)
	}
	_, _ = ui.Update(teasink.IterationCompletedMsg{TaskID: "root", Usage: usage, Delta: usage})
	if ui.UsageSnapshot().In != 0 {
		t.Fatal("live attempts folded into settled rollup")
	}
	for _, event := range []teasink.ConversationEndedMsg{
		{TaskID: "grandchild", Depth: 2, Reason: runner.TerminalError, TotalUsage: usage},
		{TaskID: "child", Depth: 1, Reason: runner.TerminalError, TotalUsage: usage},
		{TaskID: "root", Reason: runner.TerminalError, TotalUsage: usage, Error: "provider failure"},
	} {
		_, _ = ui.Update(event)
		once := ui.UsageSnapshot()
		_, _ = ui.Update(event)
		if !reflect.DeepEqual(once, ui.UsageSnapshot()) {
			t.Fatal("duplicate terminal charged again")
		}
	}
	got := ui.UsageSnapshot()
	if got.Turns != 1 || got.In != 3000 || got.Out != 300 || got.InParent != 1000 || got.OutParent != 100 || got.UnpricedTasks != 2 {
		t.Fatalf("usage = %+v", got)
	}
	if math.Abs(got.CostParentUSD-1.2) > 1e-9 || math.Abs(got.CostUSD-1.2) > 1e-9 {
		t.Fatalf("task repriced or unknown child charged as parent: %+v", got)
	}
	// A terminal with no accepted start, or with a different depth, is not ours.
	_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "unknown", TotalUsage: usage})
	if !reflect.DeepEqual(got, ui.UsageSnapshot()) {
		t.Fatal("unaccepted task charged")
	}
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "next", Provider: "openai", Model: "parent-model"})
	_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "next", Depth: 1, TotalUsage: usage})
	if !reflect.DeepEqual(got, ui.UsageSnapshot()) {
		t.Fatal("mismatched depth charged")
	}
	_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "next", Reason: runner.TerminalCancelled})
	if ui.UsageSnapshot().Turns != 2 || ui.UsageSnapshot().In != 3000 {
		t.Fatal("unreported cancellation settlement")
	}
}
