package spawn_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestAgentStatusMetadataPreservesTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		group := spawn.NewGroup(spawn.WithMaxRuntime(time.Second))
		defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
		client := &cancelClient{started: make(chan struct{})}
		id := startScopedTask(t, t.Context(), spawn.NewAsync(runner.New(client), group))
		<-client.started
		terminal, err := group.Wait(t.Context(), id)
		if err != nil || !terminal.Result.TimedOut {
			t.Fatalf("terminal timeout = %#v, %v", terminal, err)
		}
		result := executeStatus(t, t.Context(), spawn.NewStatus(group), tools.ToolParameters{"include_summary": false})
		data := assertStatusMetadata(t, result, terminal.State)
		if data["timed_out"] != true || data["reason"] != string(terminal.Result.Reason) {
			t.Fatalf("metadata lost timeout: %#v", data)
		}
	})
}

func TestAgentStatusMetadataPreservesPendingCompletion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client runner.Client
		state  spawn.AgentTaskState
	}{
		{name: "completed", client: &immediateClient{content: "private summary"}, state: spawn.AgentTaskStates.COMPLETED},
		{name: "failed", client: errorClient{}, state: spawn.AgentTaskStates.FAILED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := spawn.NewGroup(spawn.WithMaxObserved(1))
			defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
			for _, parent := range []taskscope.ID{"owner", "other"} {
				if _, err := group.Bind(t.Context(), parent); err != nil {
					t.Fatal(err)
				}
			}
			ctx := taskscope.WithID(t.Context(), "owner")
			id := startScopedTask(t, ctx, spawn.NewAsync(runner.New(tc.client), group))
			terminal, err := group.Wait(t.Context(), id)
			if err != nil || terminal.State != tc.state {
				t.Fatalf("terminal = %#v, %v", terminal, err)
			}
			before := group.Ready("owner")
			if len(before.Inputs) != 1 {
				t.Fatalf("pending completion = %#v", before)
			}
			status := spawn.NewStatus(group)
			for _, caller := range []taskscope.ID{"other", "owner", "owner"} {
				result := executeStatus(t, taskscope.WithID(t.Context(), caller), status, tools.ToolParameters{"task_id": string(id), "include_summary": false})
				data := assertStatusMetadata(t, result, tc.state)
				if data["reason"] != string(terminal.Result.Reason) || data["iterations"] != terminal.Result.Iterations || data["timed_out"] != terminal.Result.TimedOut {
					t.Fatalf("metadata lost terminal outcome: %#v", data)
				}
				if snapshot, err := group.Peek(id); err != nil || snapshot.Observed || snapshot.Admitted {
					t.Fatalf("metadata consumed result: %#v, %v", snapshot, err)
				}
				if ready := group.Ready("owner"); len(ready.Inputs) != 1 || ready.Inputs[0].Reference != before.Inputs[0].Reference {
					t.Fatalf("metadata changed automatic delivery: %#v", ready)
				}
			}
			// Omission remains a full read, including failed child diagnostics.
			full := executeStatus(t, ctx, status, tools.ToolParameters{})
			if !full.Success || full.Data.(map[string]any)["summary"] != terminal.Result.Summary || len(full.AdmissionReferences) != 1 {
				t.Fatalf("default status lost full result: %#v", full)
			}
			if diagnostic, _ := full.Data.(map[string]any)["error"].(string); diagnostic != terminal.Result.Error {
				t.Fatalf("full status diagnostic = %q, want %q", diagnostic, terminal.Result.Error)
			}
			if snapshot, err := group.Peek(id); err != nil || !snapshot.Observed || snapshot.Admitted {
				t.Fatalf("full read observation = %#v, %v", snapshot, err)
			}
			// Simulate the receiver acknowledging the complete result after capture.
			if accepted := group.Admit("owner", full.AdmissionReferences); len(accepted) != 1 {
				t.Fatalf("full result admission = %#v", accepted)
			}
			if accepted := group.Admit("owner", full.AdmissionReferences); len(accepted) != 0 {
				t.Fatal("duplicate admission was accepted")
			}
			if ready := group.Ready("owner"); len(ready.Inputs) != 0 || ready.Outstanding {
				t.Fatalf("completion still pending: %#v", ready)
			}
			metadata := executeStatus(t, ctx, status, tools.ToolParameters{"include_summary": false})
			if !metadata.Success || metadata.Data.(map[string]any)["observed"] != true || len(metadata.AdmissionReferences) != 0 {
				t.Fatalf("post-admission metadata = %#v", metadata)
			}
			reread := executeStatus(t, ctx, status, tools.ToolParameters{"include_summary": true})
			if reread.Data.(map[string]any)["summary"] != terminal.Result.Summary {
				t.Fatal("explicit full reread lost summary")
			}
		})
	}
}

func TestAgentStatusMetadataRunningAndCancelled(t *testing.T) {
	group := spawn.NewGroup()
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	parentCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := group.Bind(parentCtx, "owner"); err != nil {
		t.Fatal(err)
	}
	client := &cancelClient{started: make(chan struct{})}
	ctx := taskscope.WithID(parentCtx, "owner")
	id := startScopedTask(t, ctx, spawn.NewAsync(runner.New(client), group))
	<-client.started
	status := spawn.NewStatus(group)
	args := tools.ToolParameters{"include_summary": false}
	assertStatusMetadata(t, executeStatus(t, ctx, status, args), spawn.AgentTaskStates.RUNNING)
	cancel()
	if _, err := group.Wait(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	data := assertStatusMetadata(t, executeStatus(t, taskscope.WithID(t.Context(), "owner"), status, args), spawn.AgentTaskStates.CANCELLED)
	if data["reason"] != string(runner.TerminalCancelled) || data["timed_out"] != false {
		t.Fatalf("cancelled outcome = %#v", data)
	}
}

func TestAgentStatusMetadataDoesNotTriggerObservedPruning(t *testing.T) {
	group := spawn.NewGroup(spawn.WithMaxObserved(1))
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	start := spawn.NewAsync(runner.New(&immediateClient{content: "retained"}), group)
	status := spawn.NewStatus(group)
	for range 3 {
		id := startScopedTask(t, t.Context(), start)
		if _, err := group.Wait(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		assertStatusMetadata(t, executeStatus(t, t.Context(), status, tools.ToolParameters{"task_id": string(id), "include_summary": false}), spawn.AgentTaskStates.COMPLETED)
	}
	if len(group.List()) != 3 || len(group.Outstanding()) != 3 {
		t.Fatal("metadata reads consumed or pruned unbound results")
	}
	for _, snapshot := range group.List() {
		full := executeStatus(t, t.Context(), status, tools.ToolParameters{"task_id": string(snapshot.ID), "include_summary": true})
		if !full.Success {
			t.Fatalf("full status = %#v", full)
		}
	}
	if len(group.List()) != 1 || len(group.Outstanding()) != 0 {
		t.Fatal("full reads did not preserve observed retention policy")
	}
}

func TestAgentStatusMetadataParentScopeAndValidation(t *testing.T) {
	group := spawn.NewGroup()
	defer func() { _ = group.Close(context.WithoutCancel(t.Context())) }()
	status := spawn.NewStatus(group)
	for _, parent := range []taskscope.ID{"owner", "other"} {
		if _, err := group.Bind(t.Context(), parent); err != nil {
			t.Fatal(err)
		}
	}
	ctx := taskscope.WithID(t.Context(), "owner")
	start := spawn.NewAsync(runner.New(&immediateClient{content: "private"}), group)
	id := startScopedTask(t, ctx, start)
	if _, err := group.Wait(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{nil, "false", 0, []any{}, map[string]any{}} {
		result := executeStatus(t, ctx, status, tools.ToolParameters{"task_id": string(id), "include_summary": value})
		assertStatusValidation(t, result)
	}
	for _, args := range []tools.ToolParameters{
		{"include_summary": false},
		{"task_id": "missing", "include_summary": false},
	} {
		assertStatusValidation(t, executeStatus(t, taskscope.WithID(t.Context(), "other"), status, args))
	}
	otherID := startScopedTask(t, taskscope.WithID(t.Context(), "other"), start)
	if _, err := group.Wait(t.Context(), otherID); err != nil {
		t.Fatal(err)
	}
	data := assertStatusMetadata(t, executeStatus(t, ctx, status, tools.ToolParameters{"include_summary": false}), spawn.AgentTaskStates.COMPLETED)
	if data["task_id"] != string(id) {
		t.Fatalf("omitted ID escaped parent scope: %#v", data)
	}
	second := startScopedTask(t, ctx, start)
	if _, err := group.Wait(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	assertStatusValidation(t, executeStatus(t, ctx, status, tools.ToolParameters{"include_summary": false}))
	if snapshot, err := group.Peek(id); err != nil || snapshot.Observed {
		t.Fatalf("validation/metadata observed result: %#v, %v", snapshot, err)
	}
}

func executeStatus(t *testing.T, ctx context.Context, status *spawn.StatusTool, args tools.ToolParameters) *tools.ToolResult {
	t.Helper()
	result, err := status.Execute(ctx, tools.ToolCall{ID: "status", Arguments: args})
	if err != nil || result == nil {
		t.Fatalf("status = %#v, %v", result, err)
	}
	return result
}

func assertStatusMetadata(t *testing.T, result *tools.ToolResult, state spawn.AgentTaskState) map[string]any {
	t.Helper()
	if !result.Success || result.Error != "" || result.Err != nil || len(result.AdmissionReferences) != 0 {
		t.Fatalf("metadata result = %#v", result)
	}
	data := result.Data.(map[string]any)
	for _, field := range []string{"summary", "error"} {
		if _, found := data[field]; found {
			t.Errorf("metadata exposed %s: %#v", field, data)
		}
	}
	if data["status"] != state.String() || data["observed"] != false {
		t.Fatalf("metadata state = %#v", data)
	}
	return data
}

func assertStatusValidation(t *testing.T, result *tools.ToolResult) {
	t.Helper()
	if result.Success || result.Err == nil || result.Err.Kind != tools.Kinds.VALIDATION || len(result.AdmissionReferences) != 0 {
		t.Fatalf("status validation = %#v", result)
	}
	if strings.Contains(result.Error, "private") {
		t.Fatal("validation exposed child summary")
	}
}
