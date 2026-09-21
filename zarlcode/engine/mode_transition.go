package engine

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ToolNameSetMode requests an automatic workflow transition at the next request.
const ToolNameSetMode tools.ToolName = "set_mode"

const modePlan = "plan"

// WithReadOnlyTasks fixes an inspect-only authority ceiling for this live runner.
// Neither autonomous transitions nor manual mode changes can remove it. Plan
// artifact tools retain their existing exception. Free-text intent remains an
// instruction to the model; callers needing enforcement must select this policy.
func WithReadOnlyTasks() options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.readOnly = true; l.target.Plan = true }
}

// ModeChanged reports an applied workflow change, never an approval request.
// Generation lets consumers discard events superseded by an operator override.
type ModeChanged struct {
	TaskID     string
	Plan       bool
	Reason     string
	Generation uint64
}

// AppliedMode snapshots the effective workflow and its override generation.
// TaskID and Reason are empty; they belong to individual applied events.
func (l *LiveRunner) AppliedMode() ModeChanged {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ModeChanged{Plan: l.target.Plan || l.readOnly, Generation: l.modeGeneration}
}

type modeChangedSink interface{ ModeChanged(ModeChanged) }

type modeRequest struct {
	Mode   string `json:"mode" enum:"plan,build" doc:"Target workflow mode"`
	Reason string `json:"reason" doc:"Short reason for switching (1–256 bytes)"`
}

type pendingMode struct {
	plan       bool
	reason     string
	generation uint64
}

// modeTransition is owned by one turn. Children may borrow the parent's runner,
// so all entry points check task identity/depth and shared state is synchronized.
// Nothing pending is persisted or applied after the root Run ends.
type modeTransition struct {
	live       *LiveRunner
	group      *spawn.Group
	mu         sync.Mutex
	taskID     taskscope.ID
	generation uint64
	changes    int
	pending    *pendingMode
}

func (m *modeTransition) request(ctx context.Context, args modeRequest) (string, error) {
	var plan bool
	switch args.Mode {
	case modePlan:
		plan = true
	case "build":
	default:
		return "", tools.Validation(string(ToolNameSetMode), "mode must be plan or build")
	}
	if len(args.Reason) > 256 || strings.TrimSpace(args.Reason) == "" {
		return "", tools.Validation(string(ToolNameSetMode), "reason must contain 1–256 bytes")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if taskscope.DepthFrom(ctx) != 0 || taskscope.IDFrom(ctx) != m.taskID || m.taskID == "" {
		return "", tools.Permission(string(ToolNameSetMode), "only the owning root task may change mode")
	}
	m.live.mu.Lock()
	defer m.live.mu.Unlock()
	if m.generation != m.live.modeGeneration {
		return "", tools.Permission(string(ToolNameSetMode), "operator override superseded this request; retry from the next request")
	}
	if m.live.readOnly && !plan {
		return "", tools.Permission(string(ToolNameSetMode), "this task has an inspect-only authority ceiling")
	}
	if m.pending != nil {
		if m.pending.plan == plan {
			return "transition already pending; continue after the next request boundary", nil
		}
		return "", tools.Validation(string(ToolNameSetMode), "conflicting transition already pending")
	}
	if m.live.target.Plan == plan {
		return "already in requested mode", nil
	}
	if m.changes >= 4 {
		return "", tools.Budget(string(ToolNameSetMode), "four mode changes already applied; finish in the current mode")
	}
	// Background processes outlive dispatch. Do not announce Plan while a prior
	// shell may still be writing. The model can stop/join them and retry in Build.
	if plan && m.backgroundRunning() {
		return "", tools.Permission(string(ToolNameSetMode), "stop running background processes before entering Plan")
	}
	m.pending = &pendingMode{plan: plan, reason: args.Reason, generation: m.generation}
	return "transition pending; remaining calls in this batch are refused; reissue them after the automatic next request", nil
}

func (m *modeTransition) prompt(render runner.PromptFunc) runner.PromptFunc {
	return func(ctx context.Context, vars runner.PromptVars) (string, error) {
		if taskscope.DepthFrom(ctx) == 0 {
			if err := m.apply(ctx); err != nil {
				return "", err
			}
		}
		return render(ctx, vars)
	}
}

func (m *modeTransition) apply(ctx context.Context) error {
	m.mu.Lock()
	m.taskID = taskscope.IDFrom(ctx)
	pending := m.pending
	m.mu.Unlock()
	if pending != nil {
		// Never hold the transition lock while joining children: fallback children
		// borrow this source, and must be able to finish dispatch while we wait.
		for {
			running := false
			for _, child := range m.group.List() {
				if child.State != spawn.AgentTaskStates.RUNNING {
					continue
				}
				running = true
				if _, err := m.group.Wait(ctx, child.ID); err != nil {
					return fmt.Errorf("settle children before mode change: %w", err)
				}
			}
			if !running {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.live.mu.Lock()
	var event *ModeChanged
	if p := m.pending; p != nil && p.generation == m.live.modeGeneration {
		// A child may have started a background process while settling. Such
		// processes belong to the application, not the turn, and cannot be joined
		// by the spawn group. Fail closed rather than publish a false downgrade.
		if p.plan && m.backgroundRunning() {
			m.live.mu.Unlock()
			m.mu.Unlock()
			return tools.Permission(string(ToolNameSetMode), "background process still running after children settled; remain in Build")
		}
		m.live.target.Plan = p.plan
		m.live.modeGeneration++
		m.changes++
		event = &ModeChanged{TaskID: string(m.taskID), Plan: p.plan, Reason: p.reason, Generation: m.live.modeGeneration}
	}
	m.pending = nil
	m.generation = m.live.modeGeneration
	m.live.mu.Unlock()
	m.mu.Unlock()
	if event != nil {
		if sink, ok := m.live.sink.(modeChangedSink); ok {
			sink.ModeChanged(*event)
		}
	}
	return nil
}

func (m *modeTransition) backgroundRunning() bool {
	if m.live.pm == nil {
		return false
	}
	for _, p := range m.live.pm.List() {
		if p.Running {
			return true
		}
	}
	return false
}

type modeControlSource struct {
	tools.Source
	transition *modeTransition
	control    tools.Tool
}

func newModeControlSource(src tools.Source, transition *modeTransition) *modeControlSource {
	control := tools.New(tools.ToolSpec{
		Name:            ToolNameSetMode,
		Description:     "Switch workflow before the next model request, without approval. Plan for investigation/redesign; Build for authorized implementation, never explicit plan-only/review-only work. Call alone; later calls in this batch are refused. At most four changes per task.",
		Parameters:      tools.SchemaFor[modeRequest](),
		WorkspaceAccess: tools.WorkspaceAccesses.NONE,
		DispatchBarrier: true,
	}, transition.request)
	return &modeControlSource{Source: src, transition: transition, control: control}
}

func (s *modeControlSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) {
		for tool := range s.Source.Tools(ctx) {
			if tool.Definition().Name == ToolNameSetMode {
				continue
			}
			if !yield(tool) {
				return
			}
		}
		if taskscope.DepthFrom(ctx) == 0 {
			yield(s.control)
		}
	}
}

func (s *modeControlSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if call.ToolName == ToolNameSetMode {
		return s.control.Execute(ctx, call)
	}
	s.transition.mu.Lock()
	deferred := s.transition.pending != nil && taskscope.DepthFrom(ctx) == 0
	s.transition.mu.Unlock()
	if deferred {
		return nil, tools.Permission(string(call.ToolName), "mode transition pending: reissue this call after the next request boundary")
	}
	return s.Source.Execute(ctx, call)
}

func (s *modeControlSource) ForgetTask(id taskscope.ID) {
	s.transition.mu.Lock()
	if s.transition.taskID == id {
		s.transition.pending = nil
		s.transition.taskID = ""
		s.transition.changes = 0
	}
	s.transition.mu.Unlock()
	if f, ok := s.Source.(interface{ ForgetTask(taskscope.ID) }); ok {
		f.ForgetTask(id)
	}
}
