package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrRuntimeBusy means an admission reservation conflicts with runtime work,
// queued input, another reservation, or managed background processes.
var ErrRuntimeBusy = errors.New("live runner is not quiescent")

// ErrRuntimeClosed means shutdown has begun and no reservation can be acquired.
var ErrRuntimeClosed = errors.New("live runner is closing")

// admission counts shared mutable operations without holding a mutex across
// them. A reservation excludes even a turn waiting to acquire ContextCache.mu.
// Shared operations may nest (tool callbacks, queue injection). Conversion of a
// reservation into turn admission is atomic, unlike unlocking/relocking an RWMutex.
type runtimeAdmission struct {
	mu       sync.Mutex
	active   int
	reserved bool
	closing  bool
	drained  chan struct{}
}

func (a *runtimeAdmission) enter() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reserved || a.closing {
		return false
	}
	a.active++
	return true
}

func (a *runtimeAdmission) leave() {
	a.mu.Lock()
	a.active--
	if a.closing && a.active == 0 {
		close(a.drained)
	}
	a.mu.Unlock()
}

func (a *runtimeAdmission) reserve() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return ErrRuntimeClosed
	}
	if a.reserved || a.active != 0 {
		return ErrRuntimeBusy
	}
	a.reserved = true
	return nil
}

func (a *runtimeAdmission) startClosing() (<-chan struct{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reserved {
		return nil, ErrRuntimeBusy
	}
	if !a.closing {
		a.closing = true
		a.drained = make(chan struct{})
		if a.active == 0 {
			close(a.drained)
		}
	}
	return a.drained, nil
}

func (a *runtimeAdmission) rejection() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return ErrRuntimeClosed
	}
	return ErrRuntimeBusy
}

func (a *runtimeAdmission) release() {
	a.mu.Lock()
	a.reserved = false
	a.mu.Unlock()
}

// RuntimeReservation owns exclusive mutable-runtime admission. The caller must
// Release on every path, and must separately settle canonical event application
// and durable session writes before capturing or activating state. It does not
// lock external editors, remote services, or a borrowed process manager's other
// clients. A reservation must not be copied after use.
//
// During a reservation legacy void setters are no-ops and queue writes return
// their existing rejected/no-change result (zero ID or false). The application
// must disable these controls during its transition; MCP injection stays
// non-blocking and reports rejection with zero depth.
type RuntimeReservation struct {
	mu           sync.Mutex
	owner        *LiveRunner
	active       bool
	queuedID     int
	queuedPrompt string
}

// ReserveRuntime attempts exclusive admission without waiting. Turns (including
// setup, context commit and child drain), compaction, queued input, background
// processes, closing, and other reservations make it unavailable. Call Release
// when finished; acquisition starts no goroutine and cancels no work.
func (l *LiveRunner) ReserveRuntime() (*RuntimeReservation, error) {
	return l.reserveRuntime(0, "")
}

// ReserveQueuedTurn reserves a settled runtime for promotion of the exact queue
// head. It leaves all input in place on failure or Release; RunTurn consumes only
// this head when setup succeeds. Remaining messages may be injected in that turn.
// This reservation is for dispatch, never for branch activation.
func (l *LiveRunner) ReserveQueuedTurn(id int, prompt string) (*RuntimeReservation, error) {
	if id == 0 {
		return nil, ErrRuntimeBusy
	}
	return l.reserveRuntime(id, prompt)
}

func (l *LiveRunner) reserveRuntime(queuedID int, prompt string) (*RuntimeReservation, error) {
	if err := l.admission.reserve(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	closing := l.closing
	turnActive := l.turnDone != nil
	pm := l.pm
	l.mu.Unlock()
	if closing {
		l.admission.release()
		return nil, ErrRuntimeClosed
	}
	queue := l.queue.Snapshot()
	queueMatches := len(queue) == 0 && queuedID == 0
	if queuedID != 0 {
		queueMatches = len(queue) != 0 && queue[0].ID == queuedID && queue[0].Message.Content == prompt && queue[0].Message.Role == llm.RoleUser
	}
	if turnActive || !queueMatches {
		l.admission.release()
		return nil, ErrRuntimeBusy
	}
	if pm != nil {
		for _, process := range pm.List() {
			if process.Running {
				l.admission.release()
				return nil, ErrRuntimeBusy
			}
		}
	}
	return &RuntimeReservation{owner: l, active: true, queuedID: queuedID, queuedPrompt: prompt}, nil
}

// Release ends admission exclusion. Repeated calls are safe, including after a
// successful conversion to a turn. Concurrent snapshot/apply operations finish
// before Release returns.
func (r *RuntimeReservation) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	r.active = false
	r.owner.admission.release()
}

// Snapshot returns deeply owned context and non-secret target policy observed
// under this reservation. It does not establish an applied-event watermark.
func (r *RuntimeReservation) Snapshot() ([]llm.Message, rewind.Target, error) {
	messages, target, exact, err := r.PersistenceSnapshot()
	if err != nil {
		return nil, rewind.Target{}, err
	}
	if !exact {
		return nil, rewind.Target{}, rewind.ErrTarget
	}
	return messages, target, nil
}

// PersistenceSnapshot returns owned context and non-secret target metadata for
// ordinary durable saves. exact reports whether this route supports exact
// checkpoints; false does not prevent ordinary conversation dispatch or saving.
// Callers must not publish exact checkpoints when exact is false.
func (r *RuntimeReservation) PersistenceSnapshot() ([]llm.Message, rewind.Target, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return nil, rewind.Target{}, false, ErrRuntimeBusy
	}
	target := r.owner.RunTarget()
	return r.owner.context.snapshot(), checkpointTarget(target), r.owner.checkpointTargetSupported(target), nil
}

// PlanSnapshot returns independently owned historical plan intent under the reservation.
func (r *RuntimeReservation) PlanSnapshot() (code.Plan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return code.Plan{}, ErrRuntimeBusy
	}
	return r.owner.planStore.GetPlan(), nil
}

func checkpointTarget(target RunTarget) rewind.Target {
	return rewind.Target{Provider: target.Spec.Name, Model: target.Model, Window: target.Window, Reserve: target.Reserve, PlanMode: target.Plan, CodexEffort: target.Spec.CodexEffort}
}

// RunTurn converts this reservation into turn admission after target setup,
// without an unreserved gap. The caller must first durably save recovery input
// and context, including a BEFORE checkpoint for exact routes. This method consumes
// the reservation only once setup succeeds;
// on earlier failure the caller still owns it and must Release. Queue injection
// resumes during execution, while new reservations remain excluded through
// context commit and child drain.
func (r *RuntimeReservation) RunTurn(ctx context.Context, prompt string, attachments []llm.ContentPart) error {
	return r.runTurn(ctx, prompt, attachments)
}

func (r *RuntimeReservation) runTurn(ctx context.Context, prompt string, attachments []llm.ContentPart, historyOptions ...options.Option[runner.Runner]) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return ErrRuntimeBusy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.queuedID != 0 && (prompt != r.queuedPrompt || len(attachments) != 0) {
		return ErrRuntimeBusy
	}
	converted := false
	defer func() {
		if converted {
			r.owner.admission.leave()
		}
	}()
	return r.owner.runTurnAdmitted(ctx, runner.TaskSpec{Prompt: prompt, Attachments: llm.CloneContentParts(attachments)}, func() {
		if r.queuedID != 0 {
			r.owner.queue.mu.Lock()
			r.owner.queue.messages = r.owner.queue.messages[1:]
			r.owner.queue.mu.Unlock()
		}
		r.owner.admission.mu.Lock()
		r.owner.admission.active++
		r.owner.admission.reserved = false
		r.owner.admission.mu.Unlock()
		r.active = false
		converted = true
	}, historyOptions...)
}

// RestoreCheckpoint activates validated context and a prebuilt exact target
// while retaining current security/approval dependencies. The caller must commit
// the child transaction first. No turn is submitted. Historical plan intent is
// retained; future operational and verification assertions are cleared.
func (r *RuntimeReservation) RestoreCheckpoint(checkpoint rewind.Checkpoint, target RunTarget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.queuedID != 0 {
		return ErrRuntimeBusy
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil {
		return err
	}
	if snapshot.Workspace != r.owner.ws.Root() {
		return rewind.ErrTarget
	}
	return r.restoreConversation(snapshot.Context, snapshot.Target, snapshot.Plan, target)
}

// RestoreConversation restores a durably loaded exact session head under this
// reservation. It validates replay and target compatibility before publishing;
// unlike legacy RestoreContext it never repairs or strips historical messages.
// The caller owns canonical validation and the durable session transition.
func (r *RuntimeReservation) RestoreConversation(messages []llm.Message, savedTarget rewind.Target, plan code.Plan, target RunTarget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.queuedID != 0 {
		return ErrRuntimeBusy
	}
	return r.restoreConversation(messages, savedTarget, plan, target)
}

func (r *RuntimeReservation) restoreConversation(messages []llm.Message, savedTarget rewind.Target, savedPlan code.Plan, target RunTarget) error {
	if savedTarget != checkpointTarget(target) || !r.owner.checkpointTargetSupported(target) || r.owner.readOnly && !target.Plan {
		return rewind.ErrTarget
	}
	if err := rewind.ValidateContext(messages, savedTarget.Provider); err != nil {
		return err
	}
	// Admission excludes writers; publication excludes public component readers.
	// Acquire the context gate first so readers waiting on an active turn never
	// hold publication while that turn needs target/compaction state.
	r.owner.context.mu.Lock()
	defer r.owner.context.mu.Unlock()
	r.owner.publication.Lock()
	defer r.owner.publication.Unlock()
	r.owner.context.context = llm.CloneMessages(messages)
	r.owner.context.exact = true
	r.owner.mu.Lock()
	r.owner.target = target
	r.owner.modeGeneration++
	r.owner.mu.Unlock()
	plan := r.owner.planStore
	plan.mu.Lock()
	plan.plan = clonePlan(savedPlan)
	plan.version++
	plan.mu.Unlock()
	r.owner.operational.resetForRewind()
	return nil
}

// SupportsExactTarget reports whether a proposed constructed provider route can
// preserve exact checkpoint protection. It does not mutate the live target or
// infer compatibility from a provider name alone.
func (l *LiveRunner) SupportsExactTarget(update TargetUpdate) bool {
	return l.checkpointTargetSupported(RunTarget{Provider: update.Provider, Spec: update.Spec, Model: update.Spec.Model})
}

// Only stock replay adapters are admitted in M1. Custom endpoints/configs need
// a separately versioned route identity; never persist secret-bearing URLs or
// infer adapter compatibility from a mutable registry name alone.
func (l *LiveRunner) checkpointTargetSupported(target RunTarget) bool {
	switch target.Spec.Name {
	case "openai", "anthropic", "openai-codex":
	default:
		return false
	}
	if target.Spec.BaseURL != "" || target.Spec.Model != target.Model {
		return false
	}
	if target.Spec.Name == "openai" {
		// Native Responses capture requires a capability from the constructed
		// adapter, not just a model name or a stock-looking ProviderSpec.
		route, ok := target.Provider.(interface{ DefaultReplayCompatible(string) bool })
		if (ok && !route.DefaultReplayCompatible(target.Model)) || (!ok && openai.SupportsResponsesReplay(target.Model)) {
			return false
		}
	}
	if l.settings != nil && l.settings.Registry != nil {
		definition, err := l.settings.Registry.Parse(target.Spec.Name)
		builtin, found := backends.Builtin(target.Spec.Name)
		if err != nil || !found || !definition.Builtin || !definition.Enabled || definition.AdapterType != builtin.AdapterType || definition.BaseURL != builtin.BaseURL || definition.ReasoningHistory != builtin.ReasoningHistory {
			return false
		}
	}
	return true
}
