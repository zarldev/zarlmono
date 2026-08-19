package spawn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TaskID identifies an asynchronously running sub-agent task. It is identical
// to the child runner's TaskSpec.ID.
type TaskID string

var (
	// ErrTaskNotFound reports a request for an unknown task.
	ErrTaskNotFound = errors.New("agent task not found")
	// ErrGroupClosed reports an attempt to start work after shutdown began.
	ErrGroupClosed = errors.New("agent task group is closed")
)

// TaskResult is the immutable terminal result retained by Group.
type TaskResult struct {
	Summary    string
	Iterations int
	Reason     runner.TerminalReason
	Error      string
}

// TaskSnapshot is an immutable view of a sub-agent task.
type TaskSnapshot struct {
	ID          TaskID
	State       AgentTaskState
	Agent       string
	AgentLoaded bool
	StartedAt   time.Time
	FinishedAt  time.Time
	Observed    bool
	Result      TaskResult
}

type invocation struct {
	target      *runner.Runner
	ctx         context.Context
	spec        runner.TaskSpec
	agent       string
	agentLoaded bool
	notices     []string
	mode        SpawnMode
	lease       tools.WorkspaceLease
}

type task struct {
	snapshot TaskSnapshot
	cancel   context.CancelFunc
	done     chan struct{}
}

// Group owns asynchronous sub-agent tasks. Its owner must call Close before
// releasing the runners used by its tasks.
type Group struct {
	mu     sync.RWMutex
	closed bool
	tasks  map[TaskID]*task
	order  []TaskID
	wg     sync.WaitGroup
}

// NewGroup creates an empty, usable task group. It starts no goroutines.
func NewGroup() *Group {
	return &Group{tasks: make(map[TaskID]*task)}
}

// Start records inv as RUNNING before starting its owned goroutine.
func (g *Group) Start(ctx context.Context, inv invocation) (TaskSnapshot, error) {
	if inv.target == nil {
		return TaskSnapshot{}, errors.New("agent task target is nil")
	}
	id := TaskID(inv.spec.ID)
	if id == "" {
		return TaskSnapshot{}, errors.New("agent task id is empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return TaskSnapshot{}, ErrGroupClosed
	}
	if _, exists := g.tasks[id]; exists {
		return TaskSnapshot{}, fmt.Errorf("agent task id %q already exists", id)
	}

	// The dispatch context ends when agent_spawn returns. Group owns the child
	// from this point until Close, so preserve values while taking cancellation
	// ownership explicitly.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t := &task{
		snapshot: TaskSnapshot{
			ID:          id,
			State:       AgentTaskStates.RUNNING,
			Agent:       inv.agent,
			AgentLoaded: inv.agentLoaded,
			StartedAt:   time.Now(),
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	g.tasks[id] = t
	g.order = append(g.order, id)
	g.wg.Add(1)
	go g.run(t, runCtx, inv)
	return t.snapshot, nil
}

func (g *Group) run(t *task, ctx context.Context, inv invocation) {
	defer g.wg.Done()
	defer inv.lease.Release()
	res := inv.target.Run(ctx, inv.spec)

	summary := strings.TrimSpace(res.FinalContent)
	if summary == "" {
		summary = fmt.Sprintf("(sub-agent ended with reason=%s, no final content)", res.Reason)
	}
	parts := make([]string, 0, len(inv.notices)+1)
	for _, notice := range inv.notices {
		if notice != "" {
			parts = append(parts, notice)
		}
	}
	summary = strings.Join(append(parts, summary), "\n\n")

	state := AgentTaskStates.FAILED
	switch res.Reason {
	case runner.TerminalCompleted:
		state = AgentTaskStates.COMPLETED
	case runner.TerminalCancelled:
		state = AgentTaskStates.CANCELLED
	}
	result := TaskResult{Summary: summary, Iterations: res.Iterations, Reason: res.Reason}
	if res.Err != nil {
		result.Error = res.Err.Error()
	}

	g.mu.Lock()
	if t.snapshot.State == AgentTaskStates.RUNNING {
		t.snapshot.State = state
		t.snapshot.Result = result
		t.snapshot.FinishedAt = time.Now()
		close(t.done)
	}
	g.mu.Unlock()
}

// Await waits for taskID to reach a terminal state. Cancelling ctx interrupts
// only this wait; it never cancels the task. A terminal result is marked observed.
func (g *Group) Await(ctx context.Context, taskID TaskID) (TaskSnapshot, error) {
	g.mu.RLock()
	t, ok := g.tasks[taskID]
	g.mu.RUnlock()
	if !ok {
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrTaskNotFound, taskID)
	}
	select {
	case <-t.done:
		return g.observe(taskID)
	case <-ctx.Done():
		return TaskSnapshot{}, ctx.Err()
	}
}

// Snapshot returns the current immutable task view. A terminal result is marked
// observed because its summary is being delivered to the caller.
func (g *Group) Snapshot(taskID TaskID) (TaskSnapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.tasks[taskID]
	if !ok {
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrTaskNotFound, taskID)
	}
	if t.snapshot.State != AgentTaskStates.RUNNING {
		t.snapshot.Observed = true
	}
	return t.snapshot, nil
}

func (g *Group) observe(taskID TaskID) (TaskSnapshot, error) {
	return g.Snapshot(taskID)
}

// List returns all retained task snapshots in stable start order. Listing does
// not mark terminal summaries observed.
func (g *Group) List() []TaskSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]TaskSnapshot, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, g.tasks[id].snapshot)
	}
	return out
}

// Outstanding returns tasks that are running or terminal but not yet observed.
func (g *Group) Outstanding() []TaskSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]TaskSnapshot, 0, len(g.order))
	for _, id := range g.order {
		snapshot := g.tasks[id].snapshot
		if snapshot.State == AgentTaskStates.RUNNING || !snapshot.Observed {
			out = append(out, snapshot)
		}
	}
	return out
}

// Cancel requests cancellation and waits for the task's terminal snapshot.
func (g *Group) Cancel(ctx context.Context, taskID TaskID) (TaskSnapshot, error) {
	g.mu.RLock()
	t, ok := g.tasks[taskID]
	g.mu.RUnlock()
	if !ok {
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrTaskNotFound, taskID)
	}
	t.cancel()
	return g.Await(ctx, taskID)
}

// Close prevents new starts, cancels live tasks, and waits for every owned
// goroutine. ctx bounds the wait; a later Close may join remaining tasks.
func (g *Group) Close(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	cancels := make([]context.CancelFunc, 0, len(g.tasks))
	for _, t := range g.tasks {
		if t.snapshot.State == AgentTaskStates.RUNNING {
			cancels = append(cancels, t.cancel)
		}
	}
	g.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close agent task group: %w", ctx.Err())
	}
}

func taskData(s TaskSnapshot) map[string]any {
	data := map[string]any{
		"task_id":      string(s.ID),
		"status":       s.State.String(),
		agentField:     s.Agent,
		"agent_loaded": s.AgentLoaded,
		"started_at":   s.StartedAt,
		"observed":     s.Observed,
	}
	if !s.FinishedAt.IsZero() {
		data["finished_at"] = s.FinishedAt
		data["summary"] = s.Result.Summary
		data["iterations"] = s.Result.Iterations
		data["reason"] = string(s.Result.Reason)
		if s.Result.Error != "" {
			data["error"] = s.Result.Error
		}
	}
	return data
}

func taskID(args tools.ToolParameters) (TaskID, error) {
	id := TaskID(args.String("task_id", ""))
	if id == "" {
		return "", tools.Validation("agent task", "task_id is required")
	}
	return id, nil
}

func taskResult(call tools.ToolCall, snapshot TaskSnapshot) *tools.ToolResult {
	if snapshot.State == AgentTaskStates.RUNNING {
		return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: taskData(snapshot), ExecutedAt: time.Now()}
	}
	result := &tools.ToolResult{ToolCallID: call.ID, Success: snapshot.State == AgentTaskStates.COMPLETED, Data: taskData(snapshot), ExecutedAt: time.Now()}
	if !result.Success {
		result.Err = tools.Budget("agent task", fmt.Sprintf("sub-agent ended with reason=%s after %d iteration%s. Summary:\n%s", snapshot.Result.Reason, snapshot.Result.Iterations, pluralS(snapshot.Result.Iterations), snapshot.Result.Summary))
		result.Error = result.Err.Error()
	}
	return result
}

func prepare(ctx context.Context, call tools.ToolCall, t *Tool) (invocation, *tools.ToolResult) {
	args, derr := tools.DecodeArgs[Args](call.Arguments)
	if derr != nil {
		return invocation{}, tools.Failure(call.ID, derr)
	}
	depth := taskscope.DepthFrom(ctx)
	if depth >= t.maxDepth {
		return invocation{}, tools.Failure(call.ID, tools.Budget("agent_spawn", fmt.Sprintf("max recursion depth %d reached — flatten the work or stop calling tools", t.maxDepth)))
	}
	if args.Prompt == "" {
		return invocation{}, tools.Failure(call.ID, tools.Validation("agent_spawn", "prompt is required"))
	}
	explicitMode := argsModeExplicit(call.Arguments)
	if explicitMode && strings.TrimSpace(args.Mode) != "" && !normalizeMode(args.Mode).Valid() {
		return invocation{}, tools.Failure(call.ID, tools.Validation("agent_spawn", fmt.Sprintf("mode %q is invalid; use explore, verify, or implement", args.Mode)))
	}
	plannerNote := t.applyPlanner(ctx, &args)
	profileMode := SpawnMode("")
	if candidate, ok := findAgentCandidate(t.plannerAgents, args.Agent); ok {
		profileMode = candidate.Mode
	}
	target, agentLoaded, fallbackNotice := t.resolveTarget(args)
	if target == nil {
		return invocation{}, tools.Failure(call.ID, tools.Fatal("agent_spawn", errors.New("parent runner is nil")))
	}
	mode := t.effectiveMode(args, profileMode, explicitMode)
	runCtx := ctx
	if mode != "" {
		if wm, err := taskscope.ParseWorkMode(string(mode)); err == nil {
			runCtx = taskscope.WithWorkMode(runCtx, wm)
		}
		if t.modePolicy != nil {
			runCtx = runner.WithToolGate(runCtx, func(spec tools.ToolSpec) bool { return t.modePolicy(mode, spec) })
		}
	}
	return invocation{target: target, ctx: runCtx, spec: runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: childPromptWithMode(args.Prompt, mode), MaxIterations: t.spawnMaxIterations(args.MaxIterations), Depth: depth + 1, ParentToolCallID: call.ID.String(), AgentName: func() string {
		if agentLoaded {
			return args.Agent
		}
		return ""
	}()}, agent: args.Agent, agentLoaded: agentLoaded, notices: []string{plannerNote, fallbackNotice}, mode: mode}, nil
}
