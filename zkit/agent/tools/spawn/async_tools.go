package spawn

import (
	"context"
	"errors"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	// ToolNameAgentAwait waits for a previously spawned asynchronous task.
	ToolNameAgentAwait tools.ToolName = "agent_await"
	// ToolNameAgentStatus reports one asynchronous task's latest snapshot.
	ToolNameAgentStatus tools.ToolName = "agent_status"
	// ToolNameAgentStop requests cancellation of an asynchronous task.
	ToolNameAgentStop tools.ToolName = "agent_stop"
	// ToolNameListAgentTasks lists all tasks owned by a group.
	ToolNameListAgentTasks tools.ToolName = "list_agent_tasks"
)

const defaultAwaitTimeout = 30 * time.Second

// AsyncTool starts sub-agents owned by Group.
type AsyncTool struct {
	tool        *Tool
	group       *Group
	coordinator *tools.WorkspaceCoordinator
}

// NewAsync returns an asynchronous agent_spawn protocol bound to group. The
// caller owns group and must close it before its runners are released.
func NewAsync(tool *Tool, group *Group, coordinators ...*tools.WorkspaceCoordinator) *AsyncTool {
	var coordinator *tools.WorkspaceCoordinator
	if len(coordinators) > 0 {
		coordinator = coordinators[0]
	}
	return &AsyncTool{tool: tool, group: group, coordinator: coordinator}
}

// Definition advertises the asynchronous agent_spawn start operation.
func (*AsyncTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: ToolNameAgentSpawn, Description: "Start a focused sub-agent task and return immediately with its task_id. Use agent_await to collect its final summary, agent_status to inspect it, agent_stop to cancel it, and list_agent_tasks to inspect all tasks.", Parameters: tools.SchemaFor[Args]()}
}

// Execute validates and prepares a child using the same planner, depth, mode,
// and policy rules as Tool, then starts it in the owned group.
func (a *AsyncTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if a.tool == nil || a.group == nil {
		return tools.Failure(call.ID, tools.Fatal("agent_spawn", errors.New("async spawn tool is not configured"))), nil
	}
	inv, failure := prepare(ctx, call, a.tool)
	if failure != nil {
		return failure, nil
	}
	if a.coordinator != nil {
		access := tools.WorkspaceAccesses.WRITE
		if inv.mode == SpawnModeExplore || inv.mode == SpawnModeVerify {
			access = tools.WorkspaceAccesses.READ
		}
		lease, err := a.coordinator.Acquire(tools.WorkspaceOwner(inv.spec.ID), access)
		if err != nil {
			return tools.Failure(call.ID, tools.Budget("agent_spawn", err.Error())), nil
		}
		inv.lease = lease
	}
	snapshot, err := a.group.Start(inv.ctx, inv)
	if err != nil {
		inv.lease.Release()
		return tools.Failure(call.ID, tools.Budget("agent_spawn", err.Error())), nil
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: taskData(snapshot), ExecutedAt: time.Now()}, nil
}

// AwaitTool waits for asynchronous tasks in group.
type AwaitTool struct {
	group   *Group
	timeout time.Duration
}

// NewAwait returns an agent_await tool bound to group.
func NewAwait(group *Group, opts ...options.Option[AwaitTool]) *AwaitTool {
	t := &AwaitTool{group: group, timeout: defaultAwaitTimeout}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// WithAwaitTimeout bounds one agent_await call. A timeout returns the latest
// RUNNING snapshot and leaves the child task running. Non-positive values
// disable the bound and preserve blocking behavior for compatibility.
func WithAwaitTimeout(timeout time.Duration) options.Option[AwaitTool] {
	return func(t *AwaitTool) { t.timeout = timeout }
}

// Definition advertises agent_await.
func (*AwaitTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: ToolNameAgentAwait, Description: "Wait for an asynchronous sub-agent task and return its final summary. Omit task_id only when exactly one task is currently running; otherwise use list_agent_tasks to recover it.", Parameters: tools.SchemaFor[struct {
		TaskID         string `json:"task_id,omitempty" doc:"Task receipt ID returned by agent_spawn. May be omitted only when exactly one task is running."`
		TimeoutSeconds int    `json:"timeout_seconds,omitempty" doc:"Maximum seconds to wait before returning the latest RUNNING status. Zero uses the host-configured default."`
	}]()}
}

// Execute waits without cancelling the task if this call's context ends.
func (a *AwaitTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	id, err := resolveTaskID(a.group, call.Arguments, true)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	waitCtx := ctx
	cancel := func() {}
	timeout := a.timeout
	if seconds := call.Arguments.Int("timeout_seconds", 0); seconds > 0 {
		timeout = time.Duration(seconds) * time.Second
	}
	var timeoutCtx context.Context
	if timeout > 0 {
		timeoutCtx, cancel = context.WithTimeout(ctx, timeout)
		waitCtx = timeoutCtx
	}
	defer cancel()
	snapshot, err := a.group.Await(waitCtx, id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && timeoutCtx != nil && timeoutCtx.Err() != nil {
			snapshot, snapshotErr := a.group.Peek(id)
			if snapshotErr != nil {
				return tools.Failure(call.ID, tools.Validation("agent_await", snapshotErr.Error())), nil
			}
			if snapshot.State != AgentTaskStates.RUNNING {
				// The task and timer became ready together. Observe and report the
				// terminal result rather than claiming work is still running.
				snapshot, snapshotErr = a.group.Snapshot(id)
				if snapshotErr != nil {
					return tools.Failure(call.ID, tools.Validation("agent_await", snapshotErr.Error())), nil
				}
				return taskResult(call, snapshot), nil
			}
			data := taskData(snapshot)
			data["timed_out"] = true
			data["message"] = "task is still running; call agent_status, agent_await again, or agent_stop"
			return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: data, ExecutedAt: time.Now()}, nil
		}
		if errors.Is(err, context.Canceled) {
			return tools.Failure(call.ID, tools.Transient("agent_await", err)), nil
		}
		return tools.Failure(call.ID, tools.Budget("agent_await", err.Error())), nil
	}
	return taskResult(call, snapshot), nil
}

// StatusTool returns individual task snapshots.
type StatusTool struct{ group *Group }

// NewStatus returns an agent_status tool bound to group.
func NewStatus(group *Group) *StatusTool { return &StatusTool{group: group} }

// Definition advertises agent_status.
func (*StatusTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: ToolNameAgentStatus, Description: "Inspect the latest status of an asynchronous sub-agent task. Omit task_id only when exactly one task exists; otherwise use list_agent_tasks.", Parameters: tools.SchemaFor[struct {
		TaskID string `json:"task_id,omitempty" doc:"Task receipt ID returned by agent_spawn. May be omitted only when exactly one task exists."`
	}]()}
}

// Execute returns the current task snapshot.
func (s *StatusTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	id, err := resolveTaskID(s.group, call.Arguments, false)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	snapshot, err := s.group.Snapshot(id)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("agent_status", err.Error())), nil
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: taskData(snapshot), ExecutedAt: time.Now()}, nil
}

// StopTool cancels asynchronous tasks.
type StopTool struct{ group *Group }

// NewStop returns an agent_stop tool bound to group.
func NewStop(group *Group) *StopTool { return &StopTool{group: group} }

// Definition advertises agent_stop.
func (*StopTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: ToolNameAgentStop, Description: "Request cancellation of an asynchronous sub-agent task. Omit task_id only when exactly one task is running; otherwise use list_agent_tasks.", Parameters: tools.SchemaFor[struct {
		TaskID string `json:"task_id,omitempty" doc:"Task receipt ID returned by agent_spawn. May be omitted only when exactly one task is running."`
	}]()}
}

// Execute requests cancellation and returns the latest snapshot.
func (s *StopTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	id, err := resolveTaskID(s.group, call.Arguments, true)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	snapshot, err := s.group.Cancel(ctx, id)
	if err != nil {
		return tools.Failure(call.ID, tools.Validation("agent_stop", err.Error())), nil
	}
	if snapshot.State == AgentTaskStates.CANCELLED {
		return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: taskData(snapshot), ExecutedAt: time.Now()}, nil
	}
	return taskResult(call, snapshot), nil
}

// ListTool lists asynchronous tasks.
type ListTool struct{ group *Group }

// NewList returns a list_agent_tasks tool bound to group.
func NewList(group *Group) *ListTool { return &ListTool{group: group} }

// Definition advertises list_agent_tasks.
func (*ListTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: ToolNameListAgentTasks, Description: "List asynchronous sub-agent tasks started in this session.", Parameters: tools.SchemaFor[struct{}]()}
}

// Execute returns retained task snapshots.
func (l *ListTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if l == nil || l.group == nil {
		return tools.Failure(call.ID, tools.Fatal("list_agent_tasks", errors.New("task group is not configured"))), nil
	}
	snapshots := l.group.List()
	data := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		data = append(data, taskData(snapshot))
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: map[string]any{"tasks": data}, ExecutedAt: time.Now()}, nil
}
