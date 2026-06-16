package taskrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/code"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/skills"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

const findingsCollection = "task_findings"

// ChatClientFactory builds a ChatClient for the given model name.
// Implementations typically cache by model so the same runner is reused.
type ChatClientFactory func(model string) service.ChatClient

// Runner executes background research tasks using an iterative LLM loop.
type Runner struct {
	mu            sync.RWMutex
	chat          service.ChatClient // fallback when no factory set or profile has no model
	chatFactory   ChatClientFactory
	profiles      ProfileRegistry
	embedder      service.Embedder
	registry      *tools.Registry
	tasks         *repository.TaskRepo
	toolCallRepo  *repository.ToolCallRepo
	notifications *znotify.NotificationStore
	qdrant        *qdrant.Client
	convLock      *runner.ConversationLock
	systemPrompt  string
	agentName     string
	excludeTools  map[string]bool
	contextBudget int
	actionTools   []tools.Tool
	bus           *events.Bus
	skillSelector *service.SkillSelector
	templates     service.PromptTemplateStore
	coderFactory  CoderToolFactory
	workspaces    *repository.WorkspaceRepo

	// zkitLoop routes task execution through the zkit/agent/runner path
	// (executeTaskZkit) instead of the legacy runAgentLoop. Default on;
	// WithZkitLoop(false) is the escape hatch. See zkit_loop.go.
	zkitLoop bool

	queue chan repository.TaskID
	stop  chan struct{}
}

// Config carries the always-required dependencies for a Runner. Everything
// that's hot-swappable, optional, or defaulted belongs on Option.
type Config struct {
	Tasks         *repository.TaskRepo
	ToolCallRepo  *repository.ToolCallRepo
	Notifications *znotify.NotificationStore
	Qdrant        *qdrant.Client
	Embedder      service.Embedder
	ConvLock      *runner.ConversationLock
}

// Option customises a Runner at construction and at runtime via Reconfigure.
// The same option types cover both phases — that's why they mutate the Runner
// rather than a builder struct.
type Option func(*Runner)

// WithChatClient sets the fallback chat client used when the profile factory
// returns nothing. Safe to apply via Reconfigure for hot provider swaps.
func WithChatClient(c service.ChatClient) Option { return func(r *Runner) { r.chat = c } }

// WithChatFactory installs the per-profile chat client factory.
func WithChatFactory(f ChatClientFactory) Option { return func(r *Runner) { r.chatFactory = f } }

// WithProfiles wires the profile registry used to resolve tasks' tool sets.
func WithProfiles(p ProfileRegistry) Option { return func(r *Runner) { r.profiles = p } }

// WithRegistry points the runner at the live tool registry (used as a fallback
// when no ProfileRegistry is configured).
func WithRegistry(reg *tools.Registry) Option { return func(r *Runner) { r.registry = reg } }

// WithZkitLoop toggles the zkit/agent/runner execution path
// (executeTaskZkit). On by default — pass false as the escape hatch to
// force the legacy runAgentLoop. Tasks whose model resolves to no
// streaming provider fall back to the legacy loop regardless.
func WithZkitLoop(on bool) Option { return func(r *Runner) { r.zkitLoop = on } }

// WithActionTools supplies tools that live outside the shared tools.Registry
// (runner-lifecycle helpers like store_memory, spawn_task).
func WithActionTools(actionTools []tools.Tool) Option {
	return func(r *Runner) { r.actionTools = actionTools }
}

// WithSystemPrompt sets the prefix prepended to every task's session.
func WithSystemPrompt(p string) Option { return func(r *Runner) { r.systemPrompt = p } }

// WithAgentName sets the spoken name the agent refers to itself by.
// Stored on the runner so hot renames (via Reconfigure) take effect on
// subsequent tasks without restart.
func WithAgentName(name string) Option { return func(r *Runner) { r.agentName = name } }

// WithContextBudget overrides the default context-trimming token budget.
func WithContextBudget(b int) Option { return func(r *Runner) { r.contextBudget = b } }

// WithBus wires an event bus for task lifecycle emissions.
func WithBus(b *events.Bus) Option { return func(r *Runner) { r.bus = b } }

// WithSkillSelector installs the skill-selector the runner uses to
// inject profile-appropriate capability guides into each task's system
// prompt. nil-safe: a nil selector simply skips injection.
func WithSkillSelector(s *service.SkillSelector) Option {
	return func(r *Runner) { r.skillSelector = s }
}

// WithPromptTemplates wires the operator-editable template store used
// for report frontmatter / header / footer rendering. When nil, the
// hardcoded fallbacks in this package are used.
func WithPromptTemplates(t service.PromptTemplateStore) Option {
	return func(r *Runner) { r.templates = t }
}

// WithCoderToolFactory installs the factory used to mint workspace-bound
// code tools for coder-profile tasks.
func WithCoderToolFactory(f CoderToolFactory) Option {
	return func(r *Runner) { r.coderFactory = f }
}

// WithWorkspaces installs the workspace repository used to resolve a
// task's workspace at run time.
func WithWorkspaces(ws *repository.WorkspaceRepo) Option {
	return func(r *Runner) { r.workspaces = ws }
}

// NewRunner builds a Runner with the supplied required deps and any optional
// knobs. Each Config field must be non-nil; call sites rely on the runtime
// goroutine catching nil Qdrant/Tasks at Start time rather than here so tests
// can construct stripped-down runners.
func NewRunner(cfg Config, opts ...Option) *Runner {
	r := &Runner{
		tasks:         cfg.Tasks,
		toolCallRepo:  cfg.ToolCallRepo,
		notifications: cfg.Notifications,
		qdrant:        cfg.Qdrant,
		embedder:      cfg.Embedder,
		convLock:      cfg.ConvLock,
		contextBudget: 40000,
		zkitLoop:      true,
		excludeTools: map[string]bool{
			"start_task":    true,
			"schedule_task": true,
		},
		queue: make(chan repository.TaskID, 64),
		stop:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Reconfigure applies runtime changes. Mirrors ZarlServer.Reconfigure — used
// by admin hot-swap paths (model change, budget change, etc.).
func (r *Runner) Reconfigure(opts ...Option) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, opt := range opts {
		opt(r)
	}
}

func (r *Runner) chatClient() service.ChatClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.chat
}

func (r *Runner) budget() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.contextBudget
}

// Enqueue schedules a task for execution.
func (r *Runner) Enqueue(id repository.TaskID) {
	select {
	case r.queue <- id:
	default:
		slog.Warn("task runner queue full, dropping task", "task_id", string(id))
	}
}

// Start ensures the Qdrant collection exists, picks up any pending tasks from
// the database, then begins consuming the queue in a background goroutine.
func (r *Runner) Start(ctx context.Context) {
	if err := r.qdrant.EnsureCollection(ctx, findingsCollection, 768); err != nil {
		slog.Error("ensure qdrant collection", "collection", findingsCollection, "err", err)
	}

	// Recover tasks left in `running` by a prior crash — their runtime
	// goroutine is gone but the DB still reflects the in-flight state. Flip
	// them to `pending` so ListPending picks them up below.
	if n, err := r.tasks.ResetOrphanRunning(ctx); err != nil {
		slog.Error("reset orphan running tasks", "err", err)
	} else if n > 0 {
		slog.Info("recovered orphan running tasks", "count", n)
	}

	pending, err := r.tasks.ListPending(ctx)
	if err != nil {
		slog.Error("list pending tasks on start", "err", err)
	}
	for _, t := range pending {
		r.Enqueue(repository.TaskID(t.ID))
	}

	go r.run(ctx)
}

// Stop signals the runner to stop processing new tasks.
func (r *Runner) Stop() {
	close(r.stop)
}

func (r *Runner) run(ctx context.Context) {
	for {
		select {
		case <-r.stop:
			return
		case <-ctx.Done():
			return
		case id := <-r.queue:
			r.executeTask(ctx, id)
		}
	}
}

// taskRun bundles everything one execution needs after setup: the
// task itself, the resolved profile, a chat client, an LLM session
// pre-seeded with the task prompt, the tool-call ctx, the assembled
// tool specs, and the iteration budget. Findings accumulate on the
// run as iterations progress.
type taskRun struct {
	task      repository.Task
	resolved  ResolvedProfile
	chat      service.ChatClient
	session   *service.Session
	toolCtx   context.Context
	toolSpecs []llm.Tool
	maxIter   int
	findings  []string
}

func (r *Runner) executeTask(ctx context.Context, id repository.TaskID) {
	task, ok := r.loadAndGate(ctx, id)
	if !ok {
		return
	}

	resolved, err := r.resolveExecution(ctx, task)
	if err != nil {
		if failErr := r.tasks.SetStatus(ctx, id, "failed"); failErr != nil {
			slog.Error("set task status failed", "task_id", string(id), "err", failErr)
		}
		return
	}

	// Default path: route through zkit/agent/runner.Run when the profile's
	// model resolves to a streaming provider. Falls back to the legacy loop
	// otherwise (test fakes, non-provider-aware clients) or when
	// WithZkitLoop(false) opted out.
	if r.zkitLoop {
		if prov, ok := r.pickProvider(resolved.Model); ok {
			r.executeTaskZkit(ctx, task, resolved, prov)
			return
		}
		slog.WarnContext(ctx, "zkit loop enabled but model has no provider; using legacy loop",
			"task_id", string(id), "model", resolved.Model)
	}

	run := r.buildTaskRun(ctx, task, resolved)
	if completed := r.runAgentLoop(ctx, &run); completed {
		return
	}
	r.forceComplete(ctx, &run)
}

// loadAndGate fetches the task, accepts only pending|paused, and
// transitions it to running. Returns ok=false when any precondition
// fails — caller short-circuits.
func (r *Runner) loadAndGate(ctx context.Context, id repository.TaskID) (repository.Task, bool) {
	task, err := r.tasks.Get(ctx, id)
	if err != nil {
		slog.Error("get task", "task_id", string(id), "err", err)
		return repository.Task{}, false
	}
	if task.Status != "pending" && task.Status != "paused" {
		slog.Info("skipping task with unexpected status", "task_id", string(id), "status", task.Status)
		return repository.Task{}, false
	}
	if err := r.tasks.SetStatus(ctx, id, "running"); err != nil {
		slog.Error("set task status running", "task_id", string(id), "err", err)
		return repository.Task{}, false
	}
	return task, true
}

// resolveExecution turns a task into the ResolvedProfile that drives
// the rest of the run: profile lookup with the legacy all-tools
// fallback for tests, plus coder-workspace tool binding when the
// profile is "coder" and both factory + workspace repo are wired.
func (r *Runner) resolveExecution(ctx context.Context, task repository.Task) (ResolvedProfile, error) {
	profileName := profile.Name(task.ProfileName)
	if profileName == "" {
		profileName = profile.NameDefault
	}
	resolved, err := r.resolveProfile(ctx, task, profileName)
	if err != nil {
		slog.Error("resolve profile", "task_id", string(task.ID), "profile", profileName, "err", err)
		return ResolvedProfile{}, err
	}
	if resolved.Name == profile.NameCoder && r.coderFactory != nil && r.workspaces != nil {
		if err := r.bindCoderTools(ctx, task, &resolved); err != nil {
			return ResolvedProfile{}, err
		}
	}
	return resolved, nil
}

// resolveProfile asks the registry for a ResolvedProfile, or falls
// back to legacy all-tools behaviour when no registry is configured
// (only happens in tests).
func (r *Runner) resolveProfile(ctx context.Context, task repository.Task, name profile.Name) (ResolvedProfile, error) {
	if r.profiles != nil {
		return r.profiles.Resolve(ctx, name)
	}
	maxIter := task.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	resolved := ResolvedProfile{Resolved: profile.Resolved{Name: name, MaxIterations: maxIter}}
	r.mu.RLock()
	at := r.actionTools
	r.mu.RUnlock()
	if r.registry != nil {
		var all []tools.Tool
		for t := range r.registry.Tools(ctx) {
			all = append(all, t)
		}
		resolved.Tools = append(all, at...)
		return resolved, nil
	}
	resolved.Tools = at
	return resolved, nil
}

// bindCoderTools resolves the per-task workspace, asks the coder-tool
// factory for tools rooted there, and appends those that pass the
// coder profile's whitelist to resolved.Tools. Logs and returns an
// error if the workspace can't be opened — caller marks the task
// failed.
func (r *Runner) bindCoderTools(ctx context.Context, task repository.Task, resolved *ResolvedProfile) error {
	workspaceName := task.WorkspaceName
	if workspaceName == "" {
		workspaceName = "default"
	}
	ws, err := r.workspaces.Get(ctx, workspaceName)
	if err != nil {
		slog.Error("resolve workspace for task", "task_id", string(task.ID), "workspace", workspaceName, "err", err)
		return err
	}
	codeWS, err := code.NewWorkspace(ws.Root)
	if err != nil {
		slog.Error("build code workspace", "task_id", string(task.ID), "root", ws.Root, "err", err)
		return err
	}
	// Filter factory tools by the coder profile's gate. The registry
	// already dropped names it didn't recognise; we read the gate spec
	// directly to capture names that aren't in the global registry
	// (our code tools).
	wl := make(map[string]bool)
	if r.profiles != nil {
		if gate, gErr := r.profiles.GateFor(ctx, profile.NameCoder); gErr == nil {
			for _, n := range gate.Tools {
				wl[string(n)] = true
			}
		}
	}
	for _, ft := range r.coderFactory(codeWS) {
		if wl[ft.Definition().Name.String()] {
			resolved.Tools = append(resolved.Tools, ft)
		}
	}
	return nil
}

// pickChatClient returns the chat client for this profile's model
// override, falling back to the runner's default chat client when no
// model is set or no factory is configured.
func (r *Runner) pickChatClient(model string) service.ChatClient {
	if r.chatFactory != nil && model != "" {
		return r.chatFactory(model)
	}
	return r.chatClient()
}

// buildTaskRun assembles the run-time state used by every iteration:
// system prompt, task prompt, session, tool ctx, tool specs, and the
// iteration budget. The returned taskRun is value-copy safe (slices
// are append-friendly; ctx is immutable).
func (r *Runner) buildTaskRun(ctx context.Context, task repository.Task, resolved ResolvedProfile) taskRun {
	systemPrompt, taskPrompt := r.buildPrompts(ctx, task, resolved)
	session := service.NewSession(systemPrompt)
	session.AddUser(taskPrompt, nil)

	toolCtx := context.WithValue(ctx, service.CtxPersonName, task.PersonName)
	toolCtx = context.WithValue(toolCtx, service.CtxSessionID, task.SessionID)

	maxIter := resolved.MaxIterations
	if task.MaxIterations > 0 && task.MaxIterations < maxIter {
		maxIter = task.MaxIterations
	}

	return taskRun{
		task:      task,
		resolved:  resolved,
		chat:      r.pickChatClient(resolved.Model),
		session:   session,
		toolCtx:   toolCtx,
		toolSpecs: composeProfileToolSpecs(resolved.Tools, RunnerTools()),
		maxIter:   maxIter,
	}
}

// buildPrompts assembles the system prompt and the rendered task
// prompt. System prompt = runner default + profile prefix + memory
// context + skill context, joined by blank lines. Task prompt comes
// from the TemplateTaskPrompt template (raw goal as fallback when no
// template is registered — happens in tests).
func (r *Runner) buildPrompts(ctx context.Context, task repository.Task, resolved ResolvedProfile) (system, taskPrompt string) {
	taskPrompt = r.renderTemplate(TemplateTaskPrompt, map[string]string{"goal": task.Prompt})
	if taskPrompt == "" {
		taskPrompt = task.Prompt
	}
	system = r.systemPrompt
	system = appendBlock(system, resolved.PromptPrefix)
	// Memories: e.g. "prefers prices in GBP" so the research output
	// respects them. Sits in system prompt so the model treats it as
	// background, not a user instruction.
	system = appendBlock(system, r.loadMemoryContext(ctx, task.PersonName))
	// Skills: profile-scoped capability guides picked by semantic
	// match. Compounds with memory (e.g. "research products" skill +
	// "prefers GBP" memory shape the output without separate prompt
	// engineering).
	system = appendBlock(system, r.loadSkillContext(ctx, string(resolved.Name), task.Prompt))
	return system, taskPrompt
}

// appendBlock concatenates non-empty strings with a blank-line
// separator. Empty additions are no-ops; the first non-empty seed
// becomes the result on its own.
func appendBlock(base, addition string) string {
	if addition == "" {
		return base
	}
	if base == "" {
		return addition
	}
	return base + "\n\n" + addition
}

// runAgentLoop drives the chat-tool-chat loop until the model calls
// complete_task / pause_task, the context cancels, max iterations are
// hit, or a chat error fires. Returns true if the run should NOT fall
// through to forceComplete (early termination, errors, paused), false
// when the loop exhausted maxIter without ever completing.
func (r *Runner) runAgentLoop(ctx context.Context, run *taskRun) (completed bool) {
	for i := 0; i < run.maxIter; i++ {
		if !r.waitForConvLock(ctx) {
			return true // ctx cancelled or runner stopped
		}

		if err := run.session.TrimWithSummary(ctx, run.chat, r.budget()); err != nil {
			slog.Warn("context trimming failed", "task_id", string(run.task.ID), "err", err)
		}

		result, err := run.chat.Chat(ctx, run.session.History(), run.toolSpecs)
		if err != nil {
			slog.Error("chat for task", "task_id", string(run.task.ID), "iteration", i, "err", err)
			return true
		}

		if len(result.ToolCalls) == 0 {
			run.session.AddAssistant(result.Content)
			run.session.AddUser("Use your tools to continue researching. Call complete_task when done.", nil)
			continue
		}

		run.session.AddAssistantWithToolCalls(result.Content, result.ToolCalls)
		if r.dispatchToolCalls(ctx, run, i, result.ToolCalls) {
			return true
		}
	}
	return false
}

// waitForConvLock blocks while the live conversation holds the lock. The
// shared runner.ConversationLock uses sync.Cond, so a Release wakes the
// wait immediately — no 500ms polling. Returns false if ctx is cancelled
// or the runner is stopping (caller abandons this iteration).
func (r *Runner) waitForConvLock(ctx context.Context) bool {
	if r.convLock == nil {
		return true
	}
	// Wait honors ctx; fold r.stop into a derived ctx so a shutdown also
	// wakes it. The goroutine exits when waitCtx is done (clean return or
	// stop), so it's bounded — no fire-and-forget.
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-r.stop:
			cancel()
		case <-waitCtx.Done():
		}
	}()
	return r.convLock.Wait(waitCtx) == nil
}

// dispatchToolCalls runs every tool call from a single chat response.
// Returns true when the outer loop should terminate (complete_task or
// pause_task fired) — false means continue iterating.
func (r *Runner) dispatchToolCalls(ctx context.Context, run *taskRun, iter int, calls []service.ToolCall) (terminate bool) {
	for _, tc := range calls {
		switch tc.Function.Name {
		case ToolCompleteTask:
			r.handleCompleteTask(ctx, run, iter, tc)
			return true
		case ToolPauseTask:
			r.handlePauseTask(ctx, run, tc)
			return true
		case ToolReportProgress:
			r.handleReportProgress(ctx, run, iter, tc)
		default:
			r.handleProfileTool(ctx, run, iter, tc)
		}
	}
	return false
}

// handleCompleteTask records the final summary, persists the report,
// emits the TaskFinding event, pushes the completion notification,
// and writes the obsidian artefact. The task row is marked complete.
func (r *Runner) handleCompleteTask(ctx context.Context, run *taskRun, iter int, tc service.ToolCall) {
	summary := service.Get[string](tc.Function.Arguments, "summary")
	run.findings = append(run.findings, summary)
	r.storeFindingsInQdrant(ctx, run.task, run.findings)
	fullReport := strings.Join(run.findings, "\n\n")
	if err := r.tasks.Complete(ctx, repository.TaskID(run.task.ID), iter+1, fullReport); err != nil {
		slog.Error("complete task", "task_id", string(run.task.ID), "err", err)
	}
	obsidianPath := r.persistReport(run.toolCtx, run.resolved.Tools, run.task, fullReport)
	r.emitFinding(run.task, summary)
	r.notifications.Push(znotify.Notification{
		SessionID: run.task.SessionID,
		ToolName:  "task_runner",
		Content:   fmt.Sprintf("Task complete: %s", truncate(summary, 300)),
		Broadcast: true,
	})
	r.pushReport(run.task, fullReport, obsidianPath)
}

// handleReportProgress accumulates an interim finding, pushes the
// progress notification, persists progress on the task row, emits the
// TaskFinding event, and feeds the result back into the session so
// the next iteration sees it.
func (r *Runner) handleReportProgress(ctx context.Context, run *taskRun, iter int, tc service.ToolCall) {
	finding := service.Get[string](tc.Function.Arguments, "finding")
	run.findings = append(run.findings, finding)
	r.notifications.Push(znotify.Notification{
		SessionID: run.task.SessionID,
		ToolName:  "task_runner",
		Content:   fmt.Sprintf("Task %s [%d/%d]: %s", run.task.ID[:8], iter+1, run.maxIter, truncate(finding, 300)),
		Broadcast: true,
	})
	if err := r.tasks.UpdateProgress(ctx, repository.TaskID(run.task.ID), iter+1, finding); err != nil {
		slog.Error("update task progress", "task_id", string(run.task.ID), "err", err)
	}
	r.emitFinding(run.task, finding)
	run.session.AddToolResult(fmt.Sprintf("Progress recorded: %s", finding))
}

// handlePauseTask flips the task row to paused and pushes a
// notification. The runner exits — Enqueue will pick it back up when
// the operator resumes.
func (r *Runner) handlePauseTask(ctx context.Context, run *taskRun, tc service.ToolCall) {
	reason := service.Get[string](tc.Function.Arguments, "reason")
	if err := r.tasks.SetStatus(ctx, repository.TaskID(run.task.ID), "paused"); err != nil {
		slog.Error("pause task", "task_id", string(run.task.ID), "err", err)
	}
	r.notifications.Push(znotify.Notification{
		SessionID: run.task.SessionID,
		ToolName:  "task_runner",
		Content:   fmt.Sprintf("Task %s paused: %s", run.task.ID[:8], truncate(reason, 300)),
		Broadcast: true,
	})
}

// handleProfileTool dispatches a non-meta tool call against the
// profile's tool list, logs the call, and feeds the result back into
// the session. Calls that match an excluded tool name (start_task /
// schedule_task) get a polite refusal so the model doesn't loop.
func (r *Runner) handleProfileTool(ctx context.Context, run *taskRun, iter int, tc service.ToolCall) {
	if r.excludeTools[tc.Function.Name] {
		run.session.AddToolResult(fmt.Sprintf("tool %s is not available in background tasks", tc.Function.Name))
		return
	}
	for _, at := range run.resolved.Tools {
		if at.Definition().Name.String() != tc.Function.Name {
			continue
		}
		toolResult := r.executeProfileTool(ctx, run, iter, at, tc)
		run.session.AddToolResult(toolResult)
		return
	}
	run.session.AddToolResult(fmt.Sprintf("tool %s not available in profile %q", tc.Function.Name, run.resolved.Name))
}

// executeProfileTool runs a single tool, logs the invocation in the
// tool_calls table, and returns the string the session should see.
// Notification fires before the call so the UI shows progress while
// the tool is running.
func (r *Runner) executeProfileTool(ctx context.Context, run *taskRun, iter int, tool tools.Tool, tc service.ToolCall) string {
	r.notifications.Push(znotify.Notification{
		SessionID: run.task.SessionID,
		ToolName:  "task_runner",
		Content:   fmt.Sprintf("Task %s [%d/%d]: calling %s", run.task.ID[:8], iter+1, run.maxIter, tc.Function.Name),
		Broadcast: true,
	})
	start := time.Now()
	call := tools.ToolCall{
		ToolName:  tools.ToolName(tc.Function.Name),
		Arguments: tools.ToolParameters(tc.Function.Arguments),
	}
	result, execErr := tool.Execute(run.toolCtx, call)
	duration := time.Since(start)

	errStr := ""
	toolResult := ""
	successText := ""
	switch {
	case execErr != nil:
		errStr = execErr.Error()
		toolResult = fmt.Sprintf("tool %s error: %v", tc.Function.Name, execErr)
	case result != nil && !result.Success:
		errStr = result.Error
		toolResult = fmt.Sprintf("tool %s error: %s", tc.Function.Name, result.Error)
	default:
		successText = service.ToolResultText(result)
		toolResult = successText
	}

	argsJSON, jerr := json.Marshal(tc.Function.Arguments)
	if jerr != nil {
		argsJSON = []byte("{}")
	}
	if logErr := r.toolCallRepo.Log(ctx, repository.ToolCall{
		SessionID:  run.task.SessionID,
		ToolName:   tc.Function.Name,
		Provider:   r.registry.ProviderFor(tools.ToolName(tc.Function.Name)),
		Args:       string(argsJSON),
		Result:     successText,
		Error:      errStr,
		DurationMs: int(duration.Milliseconds()),
	}); logErr != nil {
		slog.Error("log tool call", "tool", tc.Function.Name, "err", logErr)
	}
	return toolResult
}

// emitFinding broadcasts a TaskFinding event when a bus is wired.
// Used by both complete_task and report_progress paths.
func (r *Runner) emitFinding(task repository.Task, finding string) {
	if r.bus == nil {
		return
	}
	r.bus.Emit(events.Event{
		Type: events.TaskFinding,
		Payload: events.TaskFindingPayload{
			TaskID:     string(task.ID),
			Finding:    finding,
			PersonName: task.PersonName,
		},
	})
}

// forceComplete handles the max-iterations-reached path: synthesise a
// report from the conversation if the model never called the meta
// tools, mark the task complete, persist the artefact, and push a
// "Task complete" notification (prefix matters — the frontend's
// completion matcher injects the finding back into the conversation
// so the avatar narrates the outcome).
func (r *Runner) forceComplete(ctx context.Context, run *taskRun) {
	fullReport := strings.Join(run.findings, "\n\n")
	if strings.TrimSpace(fullReport) == "" {
		fullReport = r.synthesiseReportFromHistory(ctx, run.chat, run.task, run.session.History())
	}
	summary := firstLineOr(fullReport, lastFinding(run.findings))
	r.storeFindingsInQdrant(ctx, run.task, []string{fullReport})
	if err := r.tasks.Complete(ctx, repository.TaskID(run.task.ID), run.maxIter, fullReport); err != nil {
		slog.Error("force complete task", "task_id", string(run.task.ID), "err", err)
	}
	obsidianPath := r.persistReport(run.toolCtx, run.resolved.Tools, run.task, fullReport)
	r.notifications.Push(znotify.Notification{
		SessionID: run.task.SessionID,
		ToolName:  "task_runner",
		Content:   fmt.Sprintf("Task complete: reached max iterations (%d). Last finding: %s", run.maxIter, truncate(summary, 300)),
		Broadcast: true,
	})
	r.pushReport(run.task, fullReport, obsidianPath)
}

// loadMemoryContext pulls the requester's stored memories from qdrant
// and formats them as a system-prompt fragment. Returns empty string
// when there's no person, no qdrant/embedder configured, or no stored
// memories — callers treat an empty return as "skip injection".
func (r *Runner) loadMemoryContext(ctx context.Context, personName string) string {
	if personName == "" || r.qdrant == nil || r.embedder == nil {
		return ""
	}
	memories, err := memory.LoadRecentMemories(ctx, r.qdrant, r.embedder, personName, 10)
	if err != nil {
		slog.Warn("load task memories", "person", personName, "err", err)
		return ""
	}
	if len(memories) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Personal context about the requester (%s) — respect these when producing findings:\n", personName)
	for _, m := range memories {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	return strings.TrimRight(b.String(), "\n")
}

// loadSkillContext renders the skills the selector returned for this
// (profile, query) pair as a system-prompt fragment. Returns an empty
// string when no selector is configured, the selector errors, or no
// skills match — callers treat empty as "skip injection".
func (r *Runner) loadSkillContext(ctx context.Context, profile, query string) string {
	if r.skillSelector == nil {
		return ""
	}
	selected, err := r.skillSelector.Select(ctx, profile, query)
	if err != nil {
		slog.Warn("skill selector", "profile", profile, "err", err)
		return ""
	}
	return RenderSkills(selected)
}

// RenderSkills turns a set of selected skills into the markdown block
// appended to the system prompt. Shared between taskrunner and live
// chat so both paths format skills identically.
func RenderSkills(selected []skills.Skill) string {
	if len(selected) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Relevant skills\n\n")
	for _, sk := range selected {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", sk.Name, strings.TrimSpace(sk.Markdown))
	}
	return strings.TrimRight(b.String(), "\n")
}

// synthesiseReportFromHistory asks the task's LLM to produce a markdown
// report from the full conversation transcript. Fallback for when the
// model never called report_progress / complete_task — otherwise the
// user's 20 iterations of research vanish into a blank "Task complete"
// popup. Returns empty string on any failure so the caller just emits
// a blank report (same as the old behaviour, no regression).
func (r *Runner) synthesiseReportFromHistory(ctx context.Context, chat service.ChatClient, task repository.Task, history []service.Message) string {
	var transcript strings.Builder
	for _, m := range history {
		if m.Role == "system" {
			continue
		}
		label := m.Role
		switch m.Role {
		case "user":
			label = "User"
		case "assistant":
			label = "Assistant"
		case "tool":
			label = "Tool result"
		}
		transcript.WriteString(label)
		transcript.WriteString(": ")
		transcript.WriteString(m.Content)
		transcript.WriteString("\n\n")
	}
	if transcript.Len() == 0 {
		return ""
	}

	prompt := r.renderTemplate(TemplateSynthesiseFromHist, nil)
	if prompt == "" {
		slog.Warn("synthesise report template missing", "task_id", task.ID)
		return ""
	}
	result, err := chat.Chat(ctx, []service.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Task: " + firstLineOr(task.Prompt, "Research") + "\n\nTranscript:\n\n" + transcript.String()},
	}, nil)
	if err != nil {
		slog.Warn("synthesise report from history", "task_id", task.ID, "err", err)
		return ""
	}
	return strings.TrimSpace(result.Content)
}

// composeProfileToolSpecs builds the tool spec list from the profile's resolved
// tools and the Runner's built-in control tools (complete_task, etc.).
func composeProfileToolSpecs(profileTools, runnerTools []tools.Tool) []llm.Tool {
	all := make([]tools.Tool, 0, len(profileTools)+len(runnerTools))
	all = append(all, profileTools...)
	all = append(all, runnerTools...)
	seen := make(map[string]bool, len(all))
	specs := make([]llm.Tool, 0, len(all))
	for _, t := range all {
		def := t.Definition()
		name := def.Name.String()
		if seen[name] {
			continue
		}
		seen[name] = true
		specs = append(specs, service.LLMToolFromSpec(def))
	}
	return specs
}

func (r *Runner) storeFindingsInQdrant(ctx context.Context, task repository.Task, findings []string) {
	if len(findings) == 0 {
		return
	}
	combined := strings.Join(findings, "\n\n")
	vector, err := r.embedder.Embed(ctx, combined)
	if err != nil {
		slog.Error("embed task findings", "task_id", task.ID, "err", err)
		return
	}

	point := qdrant.Point{
		ID:     uuid.New().String(),
		Vector: vector,
		Payload: map[string]any{
			"task_id":     task.ID,
			"task_prompt": task.Prompt,
			"content":     combined,
			"created_at":  time.Now().UTC().Format(time.RFC3339),
		},
	}
	if err := r.qdrant.Upsert(ctx, findingsCollection, []qdrant.Point{point}); err != nil {
		slog.Error("upsert task findings to qdrant", "task_id", task.ID, "err", err)
	}
}

// obsidianAppendTool is the well-known name of the mcp-obsidian write tool.
// Hardcoded because this is infrastructure wiring — not a prompt. If the
// task's resolved tool set contains this name, persistReport invokes it.
const obsidianAppendTool = "obsidian_append_content"

// Template keys used by the report-emission path. Values are the code
// defaults — operators can override via admin → Prompts UI and the
// overrides land through the prompt_templates table at runtime.
const (
	TemplateReportFrontmatter  = "research_report.frontmatter"
	TemplateReportHeader       = "research_report.header"
	TemplateReportFooter       = "research_report.footer"
	TemplateTaskPrompt         = "taskrunner.task_prompt"
	TemplateSynthesiseFromHist = "taskrunner.synthesise_from_history"
)

// RegisterReportTemplates seeds the code-default template content in a
// PromptTemplateStore. Call once at startup after the store is
// constructed, before the first task runs. Keys match the TemplateX
// constants; placeholders use {{name}} substitution.
func RegisterReportTemplates(store *service.MemoryPromptTemplateStore) {
	store.RegisterDefault(TemplateReportFrontmatter,
		"---\n"+
			"task_id: {{task_id}}\n"+
			"created: {{created}}\n"+
			"person: {{person_link}}\n"+
			"source: research-task\n"+
			"tags: [{{tags}}]\n"+
			"---\n\n",
	)
	store.RegisterDefault(TemplateReportHeader,
		"# {{title}}\n\n*Requested by {{person_link}} on {{date}}.*\n\n---\n\n",
	)
	store.RegisterDefault(TemplateReportFooter,
		"\n\n---\n\n**See also:** [[MOCs/Research]]{{person_backlink}}\n",
	)
	// Task bootstrap prompt — deliberately neutral about task kind
	// ("your goal", not "a research task") so the coder / default /
	// researcher profiles all make sense running under it. Names no
	// specific tools: the tool descriptions themselves tell the model
	// when to call report_progress / complete_task.
	store.RegisterDefault(TemplateTaskPrompt,
		"Your goal: {{goal}}\n\n"+
			"Work iteratively. Use the tools available to you to make progress, "+
			"record durable findings as you go, and signal completion when done.",
	)
	// Fallback prompt for the "ran out of iterations, synthesise a
	// report from the transcript" path. Operator-editable so the style
	// of salvage reports matches the rest of the vault.
	store.RegisterDefault(TemplateSynthesiseFromHist,
		"You were assigned a task and ran out of iterations before signalling completion. "+
			"Below is the conversation transcript (user prompt, your thoughts, tool calls and results). "+
			"Produce a final markdown report of what was found: headings, bullet points, links, tables where appropriate. "+
			"Be thorough but don't invent facts that weren't in the transcript. "+
			"Output markdown only — no preamble.",
	)
}

// persistReport writes the full task report to the Obsidian vault when
// the obsidian_append_content tool is available. The note is emitted
// with YAML frontmatter, tags, and [[wikilinks]] so it slots into the
// user's knowledge graph instead of dangling as an isolated file. Also
// appends a link to the per-person page and the Research MOC as a
// side-effect — every write makes the graph denser.
//
// Returns the vault-relative path on success, empty on skip or failure.
// Failures are logged but never propagated — persistence is best-effort;
// the report notification is the primary delivery channel.
func (r *Runner) persistReport(ctx context.Context, profileTools []tools.Tool, task repository.Task, markdown string) string {
	var obsidian tools.Tool
	for _, t := range profileTools {
		if t.Definition().Name.String() == obsidianAppendTool {
			obsidian = t
			break
		}
	}
	if obsidian == nil {
		return ""
	}

	now := time.Now()
	title := firstLineOr(task.Prompt, "Research task")
	dateSlug := now.Format("2006-01-02")
	notePath := fmt.Sprintf("Research/%s-%s.md", dateSlug, task.ID[:8])
	body := wikilinkKnownNames(markdown, []string{task.PersonName})

	// Only the root "research" tag is set here — sub-categorisation is
	// the LLM's job via its own markdown / frontmatter in the summary.
	// Keeping the deterministic path minimal avoids a hardcoded
	// keyword→tag map in Go that would need to be an admin surface
	// too.
	tags := []string{"research"}

	vars := map[string]string{
		"task_id":         task.ID,
		"created":         now.UTC().Format(time.RFC3339),
		"person":          task.PersonName,
		"person_link":     wikilinkOrBlank(task.PersonName),
		"person_backlink": personPageBacklink(task.PersonName),
		"title":           title,
		"date":            now.Format("2006-01-02"),
		"tags":            strings.Join(tags, ", "),
	}
	frontmatter := r.renderTemplate(TemplateReportFrontmatter, vars)
	header := r.renderTemplate(TemplateReportHeader, vars)
	footer := r.renderTemplate(TemplateReportFooter, vars)

	result, execErr := obsidian.Execute(ctx, tools.ToolCall{
		ToolName: tools.ToolName(obsidianAppendTool),
		Arguments: tools.ToolParameters{
			"filepath": notePath,
			"content":  frontmatter + header + body + footer,
		},
	})
	if execErr != nil {
		slog.Warn("persist task report to obsidian", "task_id", task.ID, "path", notePath, "err", execErr)
		return ""
	}
	if result != nil && !result.Success {
		slog.Warn("persist task report to obsidian", "task_id", task.ID, "path", notePath, "err", result.Error)
		return ""
	}
	slog.Info("persisted task report", "task_id", task.ID, "path", notePath, "tags", tags)

	// Graph maintenance: person page gets a new research entry; the MOC
	// gets a one-line index link. Both are append-only so we never need
	// to read-then-rewrite the target notes.
	appendPersonPageEntry(ctx, obsidian, task.PersonName, fmt.Sprintf("- %s — research: [[%s|%s]]", dateSlug, stripMdExt(notePath), title))
	appendMOCEntry(ctx, obsidian, "Research", fmt.Sprintf("- %s — [[%s|%s]] (by %s) %s", dateSlug, stripMdExt(notePath), title, wikilinkOrBlank(task.PersonName), tagLine(tags)))

	return notePath
}

// renderTemplate looks up a named template in the operator-editable
// store and substitutes {{vars}}. Falls back to an empty string when
// the store is unconfigured — report persistence just gets thinner
// headers rather than failing.
func (r *Runner) renderTemplate(key string, vars map[string]string) string {
	if r.templates == nil {
		return ""
	}
	return r.templates.Render(key, vars)
}

// wikilinkKnownNames wraps explicit person names in Obsidian wikilinks
// so the graph picks them up. Conservative: only wraps the first
// occurrence of each name (reduces clutter) and only whole-word
// matches (avoids mangling substrings, e.g. "Sam" inside "Samantha").
// Stops at the first empty
// or already-wikilinked name in the list.
func wikilinkKnownNames(content string, names []string) string {
	for _, n := range names {
		if n == "" {
			continue
		}
		// Skip if already linked somewhere in the content.
		if strings.Contains(content, "[["+n+"]]") {
			continue
		}
		// Whole-word replace, first occurrence only.
		idx := wordIndex(content, n)
		if idx < 0 {
			continue
		}
		content = content[:idx] + "[[" + n + "]]" + content[idx+len(n):]
	}
	return content
}

// wordIndex finds the first whole-word occurrence of needle in s, or -1.
func wordIndex(s, needle string) int {
	start := 0
	for {
		i := strings.Index(s[start:], needle)
		if i < 0 {
			return -1
		}
		abs := start + i
		before := abs == 0 || !isWordChar(s[abs-1])
		afterIdx := abs + len(needle)
		after := afterIdx == len(s) || !isWordChar(s[afterIdx])
		if before && after {
			return abs
		}
		start = abs + 1
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// wikilinkOrBlank wraps a non-empty name as [[Name]] or returns an
// empty string — convenience for report headers where a missing name
// should render as blank rather than a broken "[[]]" link.
func wikilinkOrBlank(name string) string {
	if name == "" {
		return ""
	}
	return "[[" + name + "]]"
}

// personPageBacklink produces the " · [[People/<Name>]]" suffix for
// footers, or empty string when there's no person on the task.
func personPageBacklink(name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf(" · [[People/%s]]", name)
}

func stripMdExt(path string) string {
	return strings.TrimSuffix(path, ".md")
}

func tagLine(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	var b strings.Builder
	for i, t := range tags {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "#%s", t)
	}
	return b.String()
}

// appendPersonPageEntry adds a bullet to People/<Name>.md. The file is
// created on first write; subsequent writes append. No seed header is
// included — obsidian_append_content is strictly append, so a seed
// would duplicate on every call. Humans can add a proper heading to
// the file once manually; the entries will still render and backlink
// correctly without one. Failures are logged, never propagated —
// person-page maintenance is ambient.
func appendPersonPageEntry(ctx context.Context, obsidian tools.Tool, name, line string) {
	if name == "" {
		return
	}
	path := fmt.Sprintf("People/%s.md", name)
	result, execErr := obsidian.Execute(ctx, tools.ToolCall{
		ToolName: tools.ToolName(obsidianAppendTool),
		Arguments: tools.ToolParameters{
			"filepath": path,
			"content":  line + "\n",
		},
	})
	if execErr != nil {
		slog.Warn("append person page entry", "name", name, "err", execErr)
		return
	}
	if result != nil && !result.Success {
		slog.Warn("append person page entry", "name", name, "err", result.Error)
	}
}

// appendMOCEntry appends a one-line index entry to MOCs/<category>.md,
// creating the file on first write. The MOC grows append-only; no
// regeneration needed — humans can reorder by hand if they want.
func appendMOCEntry(ctx context.Context, obsidian tools.Tool, category, line string) {
	if category == "" {
		return
	}
	path := fmt.Sprintf("MOCs/%s.md", category)
	result, execErr := obsidian.Execute(ctx, tools.ToolCall{
		ToolName: tools.ToolName(obsidianAppendTool),
		Arguments: tools.ToolParameters{
			"filepath": path,
			"content":  line + "\n",
		},
	})
	if execErr != nil {
		slog.Warn("append MOC entry", "category", category, "err", execErr)
		return
	}
	if result != nil && !result.Success {
		slog.Warn("append MOC entry", "category", category, "err", result.Error)
	}
}

// taskReportPayload is the JSON shape of a `report` notification — full
// markdown summary plus metadata for the frontend's floating report panel.
type taskReportPayload struct {
	TaskID       string `json:"task_id"`
	Title        string `json:"title"`
	Markdown     string `json:"markdown"`
	ObsidianPath string `json:"obsidian_path,omitempty"`
	PersonName   string `json:"person_name,omitempty"`
}

// pushReport broadcasts a rich report notification so the frontend can pop
// up a markdown-rendered panel. The existing "Task complete:" notification
// remains the textual cue the conversation LLM narrates — this is the visual
// companion.
func (r *Runner) pushReport(task repository.Task, markdown, obsidianPath string) {
	payload, err := json.Marshal(taskReportPayload{
		TaskID:       task.ID,
		Title:        firstLineOr(task.Prompt, "Task report"),
		Markdown:     markdown,
		ObsidianPath: obsidianPath,
		PersonName:   task.PersonName,
	})
	if err != nil {
		slog.Warn("marshal task report payload", "task_id", task.ID, "err", err)
		return
	}
	r.notifications.Push(znotify.Notification{
		SessionID: task.SessionID,
		ToolName:  "report",
		Content:   string(payload),
		Broadcast: true,
	})
}

// firstLineOr returns the first non-empty line of s, trimmed; fallback if
// s is empty.
func firstLineOr(s, fallback string) string {
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if len(trimmed) > 80 {
				trimmed = trimmed[:80] + "..."
			}
			return trimmed
		}
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func lastFinding(findings []string) string {
	if len(findings) == 0 {
		return ""
	}
	return findings[len(findings)-1]
}
