package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ReadyInput is one bounded host observation and its trusted admission identity.
// Message must preserve host provenance; it is not human steering or a tool call.
type ReadyInput struct {
	Message   llm.Message
	Reference tools.AdmissionReference
}

// ReadyInputs is an immutable parent-scoped view. Changed is an owner-controlled
// wake token read atomically with state, never a delivery channel. Outstanding
// includes running work and unadmitted results, including batch overflow.
type ReadyInputs struct {
	Inputs      []ReadyInput
	Outstanding bool
	Changed     <-chan struct{}
}

// InputSource supplies parent-scoped observations without I/O. Ready does not
// consume results. Admit is idempotent and scope checked; it records successful
// history admission, not durable delivery or model consumption. Admit returns
// only newly accepted references for accurate events. Finish atomically seals an
// idle task against late child admission; false reports work that must still be
// admitted. One Run owns admission for each parent ID. Lifecycle binding stays
// with the composition owner, outside this interface. Implementations must not
// retain request contexts.
type InputSource interface {
	Ready(taskscope.ID) ReadyInputs
	Admit(taskscope.ID, []tools.AdmissionReference) []tools.AdmissionReference
	Finish(taskscope.ID) bool
}

// WithInputSource enables tool-independent observation admission at safe run
// boundaries. The caller must bind and close each owning run's source scope.
// Unverified receiving routes should omit this option and retain explicit tools.
func WithInputSource(source InputSource) options.Option[Runner] {
	return func(r *Runner) { r.inputs = source }
}

// WaitingForInputs reports whether a live run is parked on owned work. Waiting
// spends wall-clock time, not iterations, and is not conversation settlement.
type WaitingForInputs struct {
	TaskID  taskscope.ID
	Depth   int
	Waiting bool
}

// InputsAdmitted reports successful canonical/in-memory history admission, not
// durable persistence or model consumption. Messages contains automatically
// inserted observations; explicit tool results contribute references only.
type InputsAdmitted struct {
	TaskID     taskscope.ID
	Depth      int
	Messages   []llm.Message
	References []tools.AdmissionReference
}

// InputSink observes live waiting and host-input admission without controlling it.
type InputSink interface {
	OnWaitingForInputs(context.Context, WaitingForInputs)
	OnInputsAdmitted(context.Context, InputsAdmitted)
}

func (t *taskRun) admitReady(ctx context.Context) error {
	if t.r.inputs == nil {
		return nil
	}
	ready := t.r.inputs.Ready(t.spec.ID)
	if len(ready.Inputs) == 0 {
		return nil
	}
	messages := make([]llm.Message, 0, len(ready.Inputs))
	references := make([]tools.AdmissionReference, 0, len(ready.Inputs))
	for _, input := range ready.Inputs {
		t.messages = append(t.messages, input.Message.Clone())
		messages = append(messages, input.Message.Clone())
		references = append(references, input.Reference)
	}
	return t.admitInputs(ctx, references, messages)
}

func (t *taskRun) admitInputs(ctx context.Context, references []tools.AdmissionReference, messages []llm.Message) error {
	if t.r.inputs == nil || len(references) == 0 {
		return nil
	}
	if err := t.flushHistory(ctx); err != nil {
		return err
	}
	admitted := t.r.inputs.Admit(t.spec.ID, references)
	if len(admitted) != 0 {
		t.r.sink.OnInputsAdmitted(ctx, InputsAdmitted{TaskID: t.spec.ID, Depth: t.spec.Depth,
			Messages: messages, References: admitted})
	}
	return nil
}

// waitForInputs has no goroutine or timer: Run owns the wait and cancellation.
// Read the user wake token before draining so an append cannot be lost between
// the drain and select. A root queue never exposes readiness to a child run.
func (t *taskRun) waitForInputs(ctx context.Context) (bool, error) {
	t.r.sink.OnWaitingForInputs(ctx, WaitingForInputs{TaskID: t.spec.ID, Depth: t.spec.Depth, Waiting: true})
	defer t.r.sink.OnWaitingForInputs(ctx, WaitingForInputs{TaskID: t.spec.ID, Depth: t.spec.Depth})
	for {
		if err := context.Cause(ctx); err != nil {
			return false, err
		}
		var userReady <-chan struct{}
		if steerer, ok := t.r.steerer.(ReadySteerer); ok {
			userReady = steerer.Ready(ctx)
		}
		before := len(t.messages)
		t.drainSteered(ctx)
		if len(t.messages) != before {
			return true, nil
		}
		ready := t.r.inputs.Ready(t.spec.ID)
		if len(ready.Inputs) != 0 {
			return true, nil
		}
		if !ready.Outstanding {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, context.Cause(ctx)
		case <-ready.Changed:
		case <-userReady:
		}
	}
}
