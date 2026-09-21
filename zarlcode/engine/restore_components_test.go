package engine_test

import (
	"reflect"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRestorePublishesCompleteComponents(t *testing.T) {
	live := reservationRunner(t, &requestRecordingProvider{})
	oldContext := []llm.Message{{Role: llm.RoleUser, Content: "current"}, {Role: llm.RoleAssistant, Content: "future answer"}}
	live.RestoreContext(oldContext)
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameRead, Arguments: tools.ToolParameters{"path": "future.go"}}, tools.Success("", "read"), nil)
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": "go test ./future"}}, tools.Success("", "passed", tools.NewProcessEffect("go test ./future", 0)), nil)
	live.RecordOperationalResult(tools.ToolCall{ToolName: code.ToolNameEdit}, tools.Failure("", tools.Stale("edit", "future failure")), nil)
	oldTarget, oldPlan := live.RunTarget(), live.Plan()
	oldFiles, oldTools, oldVerification, oldFailures := live.WorkingFiles(), live.TopTools(), live.Verification(), live.UnresolvedFailures()
	if len(oldFiles) == 0 || len(oldTools) == 0 || oldVerification == nil || len(oldFailures) == 0 {
		t.Fatal("empty operational fixture")
	}
	newContext := []llm.Message{{Role: llm.RoleUser, Content: "historical"}}
	newTarget := oldTarget
	newTarget.Model, newTarget.Spec.Model = "historical-model", "historical-model"
	newTarget.Window, newTarget.Reserve, newTarget.Plan = 12345, 2345, true
	saved := rewind.Target{Provider: newTarget.Spec.Name, Model: newTarget.Model, Window: newTarget.Window, Reserve: newTarget.Reserve, PlanMode: true}
	plan := code.Plan{Steps: []code.PlanStep{{Text: "historical intent", Status: code.StepStatuses.PENDING}, {Text: "recheck files", Status: code.StepStatuses.INPROGRESS}}}
	wantPlan := []compact.PlanStep{{Title: "historical intent", Status: code.StepStatuses.PENDING.String()}, {Title: "recheck files", Status: code.StepStatuses.INPROGRESS.String()}}
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			<-start
			for range 200 {
				if got := live.ContextSnapshot(); !reflect.DeepEqual(got, oldContext) && !reflect.DeepEqual(got, newContext) {
					t.Error("torn context")
				}
				if got := live.RunTarget(); !reflect.DeepEqual(got, oldTarget) && !reflect.DeepEqual(got, newTarget) {
					t.Error("torn target")
				}
				if got := live.Plan(); !reflect.DeepEqual(got, oldPlan) && !reflect.DeepEqual(got, wantPlan) {
					t.Error("torn plan")
				}
				if got := live.WorkingFiles(); len(got) != 0 && !reflect.DeepEqual(got, oldFiles) {
					t.Error("torn working files")
				}
				if got := live.TopTools(); len(got) != 0 && !reflect.DeepEqual(got, oldTools) {
					t.Error("torn tool counts")
				}
				if got := live.Verification(); got != nil && !reflect.DeepEqual(got, oldVerification) {
					t.Error("torn verification")
				}
				if got := live.UnresolvedFailures(); len(got) != 0 && !reflect.DeepEqual(got, oldFailures) {
					t.Error("torn failures")
				}
			}
		})
	}
	close(start)
	for range 100 {
		if err := r.RestoreConversation(newContext, saved, plan, newTarget); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	if !reflect.DeepEqual(live.ContextSnapshot(), newContext) || !reflect.DeepEqual(live.RunTarget(), newTarget) || !reflect.DeepEqual(live.Plan(), wantPlan) {
		t.Fatal("restored state missing")
	}
	if len(live.WorkingFiles()) != 0 || len(live.TopTools()) != 0 || live.Verification() != nil || len(live.UnresolvedFailures()) != 0 {
		t.Fatal("future operational claims survived")
	}
}
