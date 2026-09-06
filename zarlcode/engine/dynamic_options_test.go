package engine_test

import (
	"context"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type dynamicProvider struct{ name string }

func (p dynamicProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(func(llm.CompletionChunk, error) bool) {}
}

func (p dynamicProvider) Name() string { return p.name }

func TestLiveRunnerDynamicOptionsUpdateRunTarget(t *testing.T) {
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	initial := dynamicProvider{name: "initial"}
	live := engine.NewLiveRunner(initial, workspace, "model-a")

	got := live.RunTarget()
	if got.Provider != llm.Provider(initial) || got.Model != "model-a" || got.Window != engine.LiveContextWindow {
		t.Fatalf("initial RunTarget() = %#v", got)
	}

	next := dynamicProvider{name: "next"}
	live.SetProviderSpec(next, engine.ProviderSpec{Name: "openai", Model: "model-b", BaseURL: "https://example.test"})
	live.SetContextWindow(65536)
	live.SetLimits(1024, 12, 7, 3)
	live.SetPlanMode(true)

	got = live.RunTarget()
	if got.Provider != llm.Provider(next) {
		t.Fatalf("provider = %T %v, want next provider", got.Provider, got.Provider)
	}
	if got.Spec.Name != "openai" || got.Model != "model-b" || got.Window != 65536 {
		t.Fatalf("updated RunTarget() = %#v", got)
	}
	if got.Reserve != 1024 || got.MaxIter != 12 || got.SpawnMaxIter != 7 || got.SpawnDepth != 3 || !got.Plan {
		t.Fatalf("updated dynamic options = %#v", got)
	}
}

func TestLiveRunnerTargetTransitionIsAtomic(t *testing.T) {
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	providerA := dynamicProvider{name: "provider-a"}
	providerB := dynamicProvider{name: "provider-b"}
	updateA := engine.TargetUpdate{
		Provider: providerA,
		Spec:     engine.ProviderSpec{Name: "provider-a", Model: "model-a"},
		Window:   32_768,
	}
	updateB := engine.TargetUpdate{
		Provider: providerB,
		Spec:     engine.ProviderSpec{Name: "provider-b", Model: "model-b"},
		Window:   131_072,
	}
	live := engine.NewLiveRunner(providerA, workspace, "model-a")
	live.ApplyTarget(updateA)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, update := range []engine.TargetUpdate{updateA, updateB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 2_000 {
				live.ApplyTarget(update)
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 2_000 {
				target := live.RunTarget()
				isA := target.Provider.Name() == "provider-a" && target.Spec.Name == "provider-a" && target.Model == "model-a" && target.Window == 32_768
				isB := target.Provider.Name() == "provider-b" && target.Spec.Name == "provider-b" && target.Model == "model-b" && target.Window == 131_072
				if !isA && !isB {
					t.Errorf("observed partial target transition: %#v", target)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}
