package computer_test

import (
	"context"
	"testing"

	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	toolcomputer "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
)

func TestObserveToolDefinition(t *testing.T) {
	t.Parallel()

	def := toolcomputer.NewObserveTool(fakeObserver{}).Definition()
	if def.Name != toolcomputer.ToolNameComputerObserve {
		t.Fatalf("Name = %q, want %q", def.Name, toolcomputer.ToolNameComputerObserve)
	}
	if def.Mutates || def.AffectsWorkspace {
		t.Fatalf("computer_observe should not declare workspace effects: %#v", def)
	}
	if _, ok := def.Parameters.Properties["include_screenshot"]; !ok {
		t.Fatal("definition missing include_screenshot parameter")
	}
}

func TestActToolDefinition(t *testing.T) {
	t.Parallel()

	def := toolcomputer.NewActTool(fakeActor{}).Definition()
	if def.Name != toolcomputer.ToolNameComputerAct {
		t.Fatalf("Name = %q, want %q", def.Name, toolcomputer.ToolNameComputerAct)
	}
	if !def.AffectsWorkspace {
		t.Fatal("computer_act should declare AffectsWorkspace")
	}
	if _, ok := def.Parameters.Properties["action"]; !ok {
		t.Fatal("definition missing action parameter")
	}
}

func TestNewTools(t *testing.T) {
	t.Parallel()

	all := toolcomputer.NewTools(fakeObserver{}, fakeActor{})
	if len(all) != 2 {
		t.Fatalf("len(NewTools) = %d, want 2", len(all))
	}
	if all[0].Definition().Name != toolcomputer.ToolNameComputerObserve {
		t.Fatalf("first tool = %q, want %q", all[0].Definition().Name, toolcomputer.ToolNameComputerObserve)
	}
	if all[1].Definition().Name != toolcomputer.ToolNameComputerAct {
		t.Fatalf("second tool = %q, want %q", all[1].Definition().Name, toolcomputer.ToolNameComputerAct)
	}
}

type fakeObserver struct {
	obs model.Observation
	err error
}

func (f fakeObserver) Observe(context.Context, model.ObserveRequest) (model.Observation, error) {
	if f.err != nil {
		return model.Observation{}, f.err
	}
	if f.obs.Surface.Kind.IsValid() {
		return f.obs, nil
	}
	return model.Observation{Surface: model.SurfaceInfo{Kind: model.SurfaceKinds.BROWSER}}, nil
}

type fakeActor struct {
	obs model.Observation
	err error
}

func (f fakeActor) Act(context.Context, model.ActionRequest) (model.Observation, error) {
	if f.err != nil {
		return model.Observation{}, f.err
	}
	if f.obs.Surface.Kind.IsValid() {
		return f.obs, nil
	}
	return model.Observation{Surface: model.SurfaceInfo{Kind: model.SurfaceKinds.BROWSER}}, nil
}
