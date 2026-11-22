package tui_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestConfiguredObservationsRetainAcceptedChildDrain(t *testing.T) {
	t.Parallel()
	model := tui.New()
	model.SetWorkspace(t.TempDir(), "alias")
	model.SetProvider("gateway")
	_, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "gateway", Model: "alias"})
	_, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, Provider: "child-route", Model: "child-alias"})
	_, _ = model.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1})
	_, _ = model.Update(teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted,
		TotalUsage: &llm.Usage{PromptTokens: 20, CompletionTokens: 2}})
	before := model.UsageSnapshot()
	child := teasink.ProviderAttemptSettledMsg{TaskID: "child", Depth: 1, Attempt: 1,
		Execution: &llm.ExecutionObservation{GatewayDeployment: "draining-child",
			ReportedCost: llm.ReportedCost{Reported: true, Amount: 1234}}}
	_, _ = model.Update(child)
	observed := model.ProviderObservations()
	if len(observed) != 2 || observed[1].TaskID != "child" || observed[1].Provider != "child-route" ||
		observed[1].Execution.GatewayDeployment != "draining-child" {
		t.Fatalf("accepted child drain lost: %+v", observed)
	}
	if after := model.UsageSnapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("attempt changed terminal spend: before %+v, after %+v", before, after)
	}
	_, _ = model.Update(child)
	_, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "orphan", Depth: 1})
	_, _ = model.Update(teasink.ProviderAttemptSettledMsg{TaskID: "orphan", Depth: 1, Attempt: 1})
	if got := model.ProviderObservations(); !reflect.DeepEqual(got, observed) {
		t.Fatalf("duplicate or post-terminal orphan entered drain: %+v", got)
	}
	_, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "next", Provider: "next-route", Model: "next-alias"})
	child.Attempt = 2
	_, _ = model.Update(child)
	_, _ = model.Update(teasink.ProviderAttemptSettledMsg{TaskID: "orphan", Depth: 1, Attempt: 2})
	_, _ = model.Update(teasink.ProviderAttemptSettledMsg{TaskID: "next", Attempt: 1})
	if got := model.ProviderObservations(); len(got) != 1 || got[0].TaskID != "next" {
		t.Fatalf("old drain entered next root: %+v", got)
	}
}
