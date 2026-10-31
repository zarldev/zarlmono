package engine_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestLiveTargetRejectsIncompatibleHistoryBeforePublication(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "gpt-4o"})
	history := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}}
	live.RestoreContext(history)
	before := live.RunTarget()
	admission, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Release()
	if err := admission.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "claude-code", Model: "sonnet"}, Window: 200000}); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("got %v", err)
	}
	if live.RunTarget().Spec != before.Spec || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("rejected switch changed target/history")
	}
}

func TestLiveTargetUnknownBudgetDoesNotInheritParent(t *testing.T) {
	t.Parallel()
	p := &requestRecordingProvider{}
	live := reservationRunner(t, p)
	live.SetContextWindow(1_000_000)
	admission, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Release()
	if err := admission.ApplyTarget(engine.TargetUpdate{Provider: p, Spec: engine.ProviderSpec{Name: "unknown", Model: "unknown"}}); err != nil {
		t.Fatal(err)
	}
	if got := live.RunTarget().Window; got != engine.LiveContextWindow {
		t.Fatalf("window=%d", got)
	}
}
