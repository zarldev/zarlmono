package spawn_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestImplicitTaskSelectionUsesBoundParentsChildren(t *testing.T) {
	for _, name := range []string{"await", "status", "stop"} {
		t.Run(name, func(t *testing.T) {
			group := spawn.NewGroup()
			defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
			if _, err := group.Bind(t.Context(), "root"); err != nil {
				t.Fatal(err)
			}
			parentClient := &cancelClient{started: make(chan struct{})}
			parentID := startScopedTask(t, taskscope.WithID(t.Context(), "root"), spawn.NewAsync(runner.New(parentClient), group))
			<-parentClient.started
			ctx := taskscope.WithID(t.Context(), taskscope.ID(parentID))
			var tool tools.Tool
			switch name {
			case "await":
				tool = spawn.NewAwait(group)
			case "status":
				tool = spawn.NewStatus(group)
			case "stop":
				tool = spawn.NewStop(group)
			}
			// The caller's own running task must not become an implicit target.
			result, err := tool.Execute(ctx, tools.ToolCall{ID: "empty", Arguments: tools.ToolParameters{}})
			if err != nil || result.Success {
				t.Fatalf("empty parent selected itself: %#v, %v", result, err)
			}
			child := startScopedTask(t, ctx, spawn.NewAsync(runner.New(&immediateClient{content: "own child"}), group))
			if _, err := group.Wait(t.Context(), child); err != nil {
				t.Fatal(err)
			}
			result, err = tool.Execute(ctx, tools.ToolCall{ID: "own", Arguments: tools.ToolParameters{}})
			if err != nil || !result.Success {
				t.Fatalf("implicit child result = %#v, %v", result, err)
			}
			if got := result.Data.(map[string]any)["task_id"]; got != string(child) {
				t.Fatalf("selected %v, want child %s", got, child)
			}
			if parent, err := group.Peek(parentID); err != nil || parent.State != spawn.AgentTaskStates.RUNNING {
				t.Fatalf("caller was stopped: %#v, %v", parent, err)
			}
		})
	}
}

func TestExplicitCrossParentInspectionDoesNotAdmitOwnersResult(t *testing.T) {
	group := spawn.NewGroup()
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	for _, id := range []taskscope.ID{"owner", "inspector"} {
		if _, err := group.Bind(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	child := startScopedTask(t, taskscope.WithID(t.Context(), "owner"), spawn.NewAsync(runner.New(&immediateClient{content: "evidence"}), group))
	if _, err := group.Wait(t.Context(), child); err != nil {
		t.Fatal(err)
	}
	result, err := spawn.NewStatus(group).Execute(taskscope.WithID(t.Context(), "inspector"), tools.ToolCall{ID: "inspect", Arguments: tools.ToolParameters{"task_id": string(child)}})
	if err != nil || !result.Success {
		t.Fatalf("inspection = %#v, %v", result, err)
	}
	if admitted := group.Admit("inspector", result.AdmissionReferences); len(admitted) != 0 {
		t.Fatalf("cross-parent admission = %#v", admitted)
	}
	if ready := group.Ready("owner"); len(ready.Inputs) != 1 {
		t.Fatalf("owner lost result: %#v", ready)
	}
	for _, tool := range []tools.Tool{spawn.NewAwait(group), spawn.NewStop(group)} {
		result, err := tool.Execute(taskscope.WithID(t.Context(), "inspector"), tools.ToolCall{ID: "inspect-again", Arguments: tools.ToolParameters{"task_id": string(child)}})
		if err != nil || !result.Success {
			t.Fatalf("cross-parent read = %#v, %v", result, err)
		}
	}
	result, err = spawn.NewAwait(group).Execute(taskscope.WithID(t.Context(), "owner"), tools.ToolCall{ID: "own-unread", Arguments: tools.ToolParameters{}})
	if err != nil || !result.Success || result.Data.(map[string]any)["task_id"] != string(child) {
		t.Fatalf("foreign inspection consumed owner's unread result: %#v, %v", result, err)
	}
}
