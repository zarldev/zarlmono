package spawn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// TaskID identifies an asynchronously running sub-agent task. It is identical
// to the child runner's TaskSpec.ID.
type TaskID string

var (
	// ErrTaskNotFound reports a request for an unknown task.
	ErrTaskNotFound = errors.New("agent task not found")
	// ErrGroupClosed reports an attempt to start work after shutdown began.
	ErrGroupClosed = errors.New("agent task group is closed")
	// ErrTaskIDExists reports an attempted duplicate task receipt in one Group.
	ErrTaskIDExists = errors.New("agent task id already exists")
	// ErrMaxConcurrent reports admission refusal while the group is at capacity.
	ErrMaxConcurrent = errors.New("maximum concurrent sub-agents reached")
)

// TaskResult is the immutable terminal result retained by Group.
type TaskResult struct {
	Summary    string
	Iterations int
	Reason     runner.TerminalReason
	Error      string
	TimedOut   bool
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
	Admitted    bool
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
	executionID string
}

type task struct {
	snapshot    TaskSnapshot
	parent      taskscope.ID
	cancel      context.CancelCauseFunc
	done        chan struct{}
	waiters     int
	executionID string
	mode        SpawnMode
}

// Group owns asynchronous sub-agent tasks. Its owner must call Close before
// releasing the runners used by its tasks.
type Group struct {
	mu              sync.RWMutex
	closed          bool
	tasks           map[TaskID]task
	parents         map[taskscope.ID]parentState
	order           []TaskID
	wg              sync.WaitGroup
	maxConcurrent   int
	running         int
	maxObserved     int
	maxRuntime      time.Duration
	completionBytes int
	joinOnce        sync.Once
	joined          chan struct{}
}

// NewGroup constructs an idle task owner. It starts no goroutines; the caller
// must close the group before releasing dependencies borrowed by its children.
func NewGroup(opts ...options.Option[Group]) *Group {
	g := &Group{tasks: make(map[TaskID]task), parents: make(map[taskscope.ID]parentState), maxObserved: 32, completionBytes: 16 << 10, joined: make(chan struct{})}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// WithMaxConcurrent caps simultaneously running children in a group. Zero or
// negative values leave concurrency unbounded.
func WithMaxConcurrent(n int) options.Option[Group] {
	return func(g *Group) { g.maxConcurrent = n }
}

// WithMaxObserved caps retained terminal tasks whose summaries have already
// been delivered. The oldest observed terminals are evicted first. Running and
// unobserved terminal tasks are never evicted. Non-positive values disable this cap.
func WithMaxObserved(n int) options.Option[Group] {
	return func(g *Group) { g.maxObserved = n }
}

// WithMaxRuntime bounds each child task's total lifetime. A non-positive value
// leaves runtime unbounded; Group.Close still owns cancellation and joining.
func WithMaxRuntime(timeout time.Duration) options.Option[Group] {
	return func(g *Group) { g.maxRuntime = timeout }
}

// Start records inv as RUNNING before starting its owned goroutine.
func (g *Group) Start(ctx context.Context, inv invocation) (TaskSnapshot, error) {
	id := TaskID(inv.spec.ID)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return TaskSnapshot{}, ErrGroupClosed
	}
	if err := context.Cause(ctx); err != nil {
		return TaskSnapshot{}, fmt.Errorf("start agent task: %w", err)
	}
	parentID := taskscope.IDFrom(ctx)
	parent, bound := g.parents[parentID]
	if bound && parent.sealed {
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrParentClosed, parentID)
	}
	if _, exists := g.tasks[id]; exists {
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrTaskIDExists, id)
	}
	if g.maxConcurrent > 0 && g.running >= g.maxConcurrent {
		return TaskSnapshot{}, fmt.Errorf("%w (%d); await or stop a running task before spawning another", ErrMaxConcurrent, g.maxConcurrent)
	}

	// Dispatch owns policy-bearing values, not the child lifetime. The group
	// owns cancellation; a bound parent supplies its deadline and cancellation.
	baseCtx := context.WithoutCancel(ctx)
	var deadline time.Time
	if bound {
		deadline = parent.deadline
	}
	if g.maxRuntime > 0 {
		limit := time.Now().Add(g.maxRuntime)
		if deadline.IsZero() || limit.Before(deadline) {
			deadline = limit
		}
	}
	endDeadline := func() {}
	if !deadline.IsZero() {
		baseCtx, endDeadline = context.WithDeadline(baseCtx, deadline)
	}
	runCtx, cancelRun := context.WithCancelCause(baseCtx)
	cancel := func(cause error) { cancelRun(cause); endDeadline() }
	scope := g.bindLocked(runCtx, inv.spec.ID)
	t := task{
		snapshot: TaskSnapshot{
			ID:          id,
			State:       AgentTaskStates.RUNNING,
			Agent:       inv.agent,
			AgentLoaded: inv.agentLoaded,
			StartedAt:   time.Now(),
		},
		executionID: inv.executionID,
		mode:        inv.mode,
		parent:      parentID,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	g.tasks[id] = t
	g.order = append(g.order, id)
	g.running++
	g.signalLocked(parentID)
	g.wg.Add(1)
	go g.run(runCtx, inv, scope, cancel)
	return t.snapshot, nil
}

func (g *Group) run(ctx context.Context, inv invocation, scope ParentScope, cancel context.CancelCauseFunc) {
	defer g.wg.Done()
	defer cancel(context.Canceled)
	res := runChild(ctx, inv)
	// A child cannot release its runner's dependencies while descendants still
	// use them. Cancellation bounds the run, not its owner's cleanup obligation.
	_ = scope.Close(context.WithoutCancel(ctx))

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
	timedOut := errors.Is(context.Cause(ctx), context.DeadlineExceeded)
	if timedOut {
		state = AgentTaskStates.FAILED
	}
	if res.Err == nil && context.Cause(ctx) != nil {
		res.Err = context.Cause(ctx)
	}
	result := TaskResult{Summary: summary, Iterations: res.Iterations, Reason: res.Reason, TimedOut: timedOut}
	if res.Err != nil {
		result.Error = res.Err.Error()
	}

	g.mu.Lock()
	g.running--
	id := TaskID(inv.spec.ID)
	t := g.tasks[id]
	if t.snapshot.State == AgentTaskStates.RUNNING {
		t.snapshot.State = state
		t.snapshot.Result = result
		t.snapshot.FinishedAt = time.Now()
		// Terminal records retain evidence, not the child operation context.
		t.cancel = nil
		g.tasks[id] = t
		close(t.done)
		g.signalLocked(t.parent)
	}
	g.mu.Unlock()
}

func runChild(ctx context.Context, inv invocation) runner.TaskResult {
	var result runner.TaskResult
	var recovered any
	var stack []byte
	func() {
		defer func() {
			if recovered = recover(); recovered != nil {
				stack = debug.Stack()
			}
		}()
		result = inv.target.Run(ctx, inv.spec)
	}()
	if recovered == nil {
		return result
	}
	err := fmt.Errorf("sub-agent panic: %v", recovered)
	slog.ErrorContext(context.WithoutCancel(ctx), "spawn: child runner panicked",
		"task_id", inv.spec.ID,
		"agent", inv.agent,
		"err", err,
		"stack", string(stack),
	)
	return runner.TaskResult{ID: inv.spec.ID, Reason: runner.TerminalError, Err: err}
}

// Await waits for taskID to reach a terminal state. Cancelling ctx interrupts
// only this wait; it never cancels the task. A terminal result is marked observed
// only for its owning parent or an unbound legacy caller.
func (g *Group) Await(ctx context.Context, taskID TaskID) (TaskSnapshot, error) {
	return g.wait(ctx, taskID, g.observes(ctx, taskID))
}

// Wait waits for taskID to reach a terminal state without marking its result
// observed. Cancelling ctx interrupts only the wait; it never cancels the task.
func (g *Group) Wait(ctx context.Context, taskID TaskID) (TaskSnapshot, error) {
	return g.wait(ctx, taskID, false)
}

func (g *Group) observes(ctx context.Context, id TaskID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	parent := taskscope.IDFrom(ctx)
	_, bound := g.parents[parent]
	return !bound || g.tasks[id].parent == parent
}

func (g *Group) snapshotFor(ctx context.Context, id TaskID) (TaskSnapshot, error) {
	if g.observes(ctx, id) {
		return g.Snapshot(id)
	}
	return g.Peek(id)
}

func (g *Group) wait(ctx context.Context, taskID TaskID, observe bool) (TaskSnapshot, error) {
	g.mu.Lock()
	t, ok := g.tasks[taskID]
	if !ok {
		g.mu.Unlock()
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrTaskNotFound, taskID)
	}
	// A pending waiter pins the value until its snapshot has been read. Unlike
	// retaining an element pointer, this cannot outlive the owner's record.
	t.waiters++
	g.tasks[taskID] = t
	g.mu.Unlock()
	defer g.releaseWaiter(taskID)
	select {
	case <-t.done:
		if observe {
			return g.Snapshot(taskID)
		}
		return g.Peek(taskID)
	case <-ctx.Done():
		return TaskSnapshot{}, ctx.Err()
	}
}

func (g *Group) releaseWaiter(taskID TaskID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.tasks[taskID]
	t.waiters--
	g.tasks[taskID] = t
	g.pruneObservedLocked("")
}

// Peek returns the current immutable task view without marking a terminal
// result observed. It is used by bounded waits that time out while work runs.
func (g *Group) Peek(taskID TaskID) (TaskSnapshot, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	t, ok := g.tasks[taskID]
	if !ok {
		return TaskSnapshot{}, fmt.Errorf("%w: %q", ErrTaskNotFound, taskID)
	}
	return t.snapshot, nil
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
		g.tasks[taskID] = t
		g.pruneObservedLocked(taskID)
	}
	return t.snapshot, nil
}

func (g *Group) pruneObservedLocked(preserve TaskID) {
	if g.maxObserved <= 0 {
		return
	}
	observed := 0
	for _, id := range g.order {
		t := g.tasks[id]
		if t.snapshot.Observed && t.waiters == 0 && !g.admissionPendingLocked(t) {
			observed++
		}
	}
	remove := observed - g.maxObserved
	if remove <= 0 {
		return
	}
	kept := g.order[:0]
	for _, id := range g.order {
		t := g.tasks[id]
		if remove > 0 && id != preserve && t.waiters == 0 && t.snapshot.Observed && !g.admissionPendingLocked(t) {
			delete(g.tasks, id)
			remove--
			continue
		}
		kept = append(kept, id)
	}
	g.order = kept
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
	if t.snapshot.State == AgentTaskStates.RUNNING {
		t.cancel(context.Canceled)
	}
	return g.Await(ctx, taskID)
}

// Close prevents new starts, cancels live tasks, and waits for every owned
// goroutine. ctx bounds the wait; a later Close may join remaining tasks.
func (g *Group) Close(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	cancels := make([]context.CancelCauseFunc, 0, len(g.tasks))
	parents := make([]ParentScope, 0, len(g.parents))
	for id, parent := range g.parents {
		parent.sealed = true
		g.parents[id] = parent
		parents = append(parents, ParentScope{group: g, id: id})
	}
	for _, t := range g.tasks {
		if t.snapshot.State == AgentTaskStates.RUNNING {
			cancels = append(cancels, t.cancel)
		}
	}
	g.mu.Unlock()
	for _, cancel := range cancels {
		cancel(context.Canceled)
	}

	g.joinOnce.Do(func() {
		go func() {
			for _, parent := range parents {
				_ = parent.Close(context.WithoutCancel(ctx))
			}
			g.wg.Wait()
			close(g.joined)
		}()
	})
	select {
	case <-g.joined:
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
		data["timed_out"] = s.Result.TimedOut
		if s.Result.Error != "" {
			data["error"] = s.Result.Error
		}
	}
	return data
}

// implicitTasks limits omitted-ID selection to a bound caller's own children.
// Explicit IDs still permit tree inspection; unbound legacy callers retain the
// group-wide tool protocol.
func (g *Group) implicitTasks(parentID taskscope.ID) []TaskSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, bound := g.parents[parentID]
	var snapshots []TaskSnapshot
	for _, id := range g.order {
		t := g.tasks[id]
		if !bound || t.parent == parentID {
			snapshots = append(snapshots, t.snapshot)
		}
	}
	return snapshots
}

func resolveTaskID(group *Group, parentID taskscope.ID, args tools.ToolParameters, preferRunning bool) (TaskID, error) {
	if id := TaskID(args.String("task_id", "")); id != "" {
		return id, nil
	}
	if group == nil {
		return "", tools.Fatal("agent task", errors.New("task group is not configured"))
	}
	tasks := group.implicitTasks(parentID)
	if len(tasks) == 0 {
		return "", tools.Validation("agent task", "task_id is required; no agent tasks exist")
	}
	if !preferRunning && len(tasks) == 1 {
		return tasks[0].ID, nil
	}
	if preferRunning {
		var running TaskID
		var unreadTerminal TaskID
		unreadTerminalCount := 0
		for _, task := range tasks {
			if task.State == AgentTaskStates.RUNNING {
				if running != "" {
					return "", tools.Validation("agent task", "task_id is required because multiple tasks are running; call list_agent_tasks to recover it")
				}
				running = task.ID
				continue
			}
			if !task.Observed {
				unreadTerminal = task.ID
				unreadTerminalCount++
			}
		}
		if running != "" {
			return running, nil
		}
		if unreadTerminalCount == 1 {
			return unreadTerminal, nil
		}
		if unreadTerminalCount > 1 {
			return "", tools.Validation("agent task", "task_id is required because multiple terminal results are unread; call list_agent_tasks to recover it")
		}
		return "", tools.Validation("agent task", "task_id is required because no task is running and no terminal result is unread")
	}
	return "", tools.Validation("agent task", "task_id is required because multiple tasks exist; call list_agent_tasks to recover it")
}

func taskReferences(snapshot TaskSnapshot) []tools.AdmissionReference {
	if snapshot.State == AgentTaskStates.RUNNING {
		return nil
	}
	return []tools.AdmissionReference{completionReference(snapshot.ID)}
}

func taskResult(call tools.ToolCall, snapshot TaskSnapshot) *tools.ToolResult {
	if snapshot.State == AgentTaskStates.RUNNING {
		return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: taskData(snapshot), ExecutedAt: time.Now()}
	}
	result := &tools.ToolResult{ToolCallID: call.ID, Success: snapshot.State == AgentTaskStates.COMPLETED, Data: taskData(snapshot), ExecutedAt: time.Now()}
	result.AdmissionReferences = taskReferences(snapshot)
	if !result.Success {
		result.Err = terminalToolError("agent task", snapshot.Result.Reason, snapshot.Result.Iterations, snapshot.Result.Summary, snapshot.Result.Error, snapshot.Result.TimedOut)
		result.Error = result.Err.Error()
	}
	return result
}

func terminalToolError(op string, reason runner.TerminalReason, iterations int, summary, detail string, timedOut bool) *tools.Error {
	message := fmt.Sprintf("sub-agent ended with reason=%s after %d iteration%s. Summary:\n%s", reason, iterations, pluralS(iterations), summary)
	if timedOut {
		return tools.Budget(op, message+"\nThe configured maximum child runtime was exhausted.")
	}
	switch reason {
	case runner.TerminalMaxIterations:
		return tools.Budget(op, message)
	case runner.TerminalCancelled:
		if detail != "" {
			message += "\nCancellation: " + detail
		}
		return tools.Transient(op, errors.New(message))
	case runner.TerminalError:
		if detail != "" {
			message += "\nError: " + detail
		}
		return tools.Fatal(op, errors.New(message))
	default:
		return tools.Fatal(op, errors.New(message))
	}
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
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return invocation{}, tools.Failure(call.ID, tools.Validation("agent_spawn", "prompt is required"))
	}
	if args.MaxIterations < 0 {
		return invocation{}, tools.Failure(call.ID, tools.Validation("agent_spawn", "max_iterations must be non-negative"))
	}
	explicitMode := argsModeExplicit(call.Arguments)
	if explicitMode && strings.TrimSpace(args.Mode) != "" && !normalizeMode(args.Mode).Valid() {
		return invocation{}, tools.Failure(call.ID, tools.Validation("agent_spawn", fmt.Sprintf("mode %q is invalid; use explore, verify, or implement", args.Mode)))
	}
	t.applyDefaultAgent(&args)
	plannerNote := ""
	if t.fallback == FallbackPlanner {
		plannerNote = t.applyPlanner(ctx, &args)
	}
	if err := t.strictRoutingError(args); err != nil {
		return invocation{}, tools.Failure(call.ID, err)
	}
	profileMode := SpawnMode("")
	if candidate, ok := findAgentCandidate(t.plannerAgents, args.Agent); ok {
		profileMode = candidate.Mode
	}
	target, agentLoaded, fallbackNotice := t.resolveTarget(args)
	if t.fallback == FallbackError && !agentLoaded && strings.TrimSpace(args.Agent) != "" {
		return invocation{}, tools.Failure(call.ID, tools.Validation("agent_spawn", fmt.Sprintf("agent %q could not be loaded and fallback=error", strings.TrimSpace(args.Agent))))
	}
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
	return invocation{target: target, ctx: runCtx, spec: runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: childPromptWithMode(args.Prompt, mode), MaxIterations: t.spawnMaxIterations(mode, args.MaxIterations), Depth: depth + 1, ParentToolCallID: call.ID.String(), AgentName: func() string {
		if agentLoaded {
			return args.Agent
		}
		return ""
	}(), ParentExecutionID: call.ExecutionID}, agent: args.Agent, agentLoaded: agentLoaded, notices: []string{plannerNote, fallbackNotice}, mode: mode}, nil
}
