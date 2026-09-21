package engine_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestReservedRestoreRejectsContradictoryModelWithoutMutation(t *testing.T) {
	live := reservationRunner(t, &requestRecordingProvider{})
	workspace := liveWorkspace(t, live)
	prior := []llm.Message{{Role: llm.RoleUser, Content: "current"}}
	live.RestoreContext(prior)
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	_, target, err := r.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	builder := transcript.NewBuilder()
	builder.AddUser("historical")
	builder.AppendAssistant("settled", "", "historical answer")
	builder.FinishTurn("settled")
	canonical, err := builder.Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := rewind.Capture(rewind.CaptureInput{
		ID: "checkpoint", SessionID: "source", Workspace: workspace,
		Boundary: rewind.Boundary{PromptID: "selected", SettledTurnID: "settled", EventWatermark: canonical.Revision()}, Transcript: canonical,
		Context: []llm.Message{{Role: llm.RoleUser, Content: "historical"}, {Role: llm.RoleAssistant, Content: "historical answer"}}, Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	original := live.RunTarget()
	contradictory := original
	contradictory.Spec.Model = "contradictory"
	if err := r.RestoreCheckpoint(checkpoint, contradictory); !errors.Is(err, rewind.ErrTarget) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live.RunTarget(), original) || !reflect.DeepEqual(live.ContextSnapshot(), prior) {
		t.Fatal("rejected restore mutated runtime")
	}

	// Public readers remain usable during repeated exclusive publication. The
	// reservation is the multi-read snapshot boundary; individual getters do not
	// promise that two calls straddling a restore describe the same generation.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				live.ContextSnapshot()
				live.RunTarget()
				live.Plan()
				live.WorkingFiles()
				live.TopTools()
				live.Verification()
				live.UnresolvedFailures()
			}
		})
	}
	for range 100 {
		if err := r.RestoreCheckpoint(checkpoint, original); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
}
