package engine_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLiveRunnerContextWindow(t *testing.T) {
	t.Parallel()

	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "model")
	if got := live.RunTarget().Window; got != engine.LiveContextWindow {
		t.Fatalf("default context window = %d, want %d", got, engine.LiveContextWindow)
	}

	live.SetContextWindow(131_072)
	if got := live.RunTarget().Window; got != 131_072 {
		t.Fatalf("context window = %d, want 131072", got)
	}

	live.SetContextWindow(0)
	live.SetContextWindow(-1)
	if got := live.RunTarget().Window; got != 131_072 {
		t.Fatalf("context window after non-positive updates = %d, want 131072", got)
	}
}
