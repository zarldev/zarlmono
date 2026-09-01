package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLiveRunnerLimits(t *testing.T) {
	t.Parallel()

	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "model")
	live.SetLimits(1_024, 12, 7, 3)

	target := live.RunTarget()
	if target.Reserve != 1_024 {
		t.Errorf("reserve = %d, want 1024", target.Reserve)
	}
	if target.MaxIter != 12 {
		t.Errorf("max iterations = %d, want 12", target.MaxIter)
	}
	if target.SpawnMaxIter != 7 {
		t.Errorf("spawn max iterations = %d, want 7", target.SpawnMaxIter)
	}
	if target.SpawnDepth != 3 {
		t.Errorf("spawn depth = %d, want 3", target.SpawnDepth)
	}

	live.SetLimits(0, 0, 0, 0)
	target = live.RunTarget()
	if target.Reserve != 0 || target.MaxIter != 0 || target.SpawnMaxIter != 0 || target.SpawnDepth != 0 {
		t.Fatalf("cleared limits = %+v, want zero values", target)
	}
}
