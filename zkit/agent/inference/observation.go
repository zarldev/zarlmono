package inference

import (
	"context"
	"errors"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Observation is owned evidence for one admitted invocation, independent of
// accounting. Operation, Provider and Model are host-selected labels. Usage and
// sidecars are independently optional latest snapshots, never merged or summed.
// Nil means absent, distinct from present unknown or known zero. Outcome describes
// stream consumption, not semantic response validity. No response text is retained.
// A value copy borrows pointers; Clone returns an independent snapshot.
type Observation struct {
	Operation, Provider, Model string
	TaskID                     taskscope.ID
	Depth                      int
	Usage                      *llm.Usage
	PricingUsage               *llm.PricingUsage
	Execution                  *llm.ExecutionObservation
	Duration                   time.Duration
	Outcome                    StreamOutcome
}

// Clone returns an observation with independent usage and sidecar storage.
func (o Observation) Clone() Observation {
	if o.Usage != nil {
		usage := *o.Usage
		o.Usage = &usage
	}
	if o.PricingUsage != nil {
		usage := o.PricingUsage.Clone()
		o.PricingUsage = &usage
	}
	if o.Execution != nil {
		execution := o.Execution.Clone()
		o.Execution = &execution
	}
	return o
}

// InvokeObserved admits and consumes one invocation through the same accounting
// path as Invoke. Only admission errors are returned. Observed usage and sidecars
// are cloned during yield before accounting and downstream callbacks. Chunks and
// the downstream boolean are forwarded unchanged.
// observed receives one owned snapshot synchronously after accounting settlement,
// outside its locks, including on panic unwind (without recovering the panic).
// Refused admission invokes neither source nor observed. consume must synchronously
// consume or stop the one-shot stream. Duration ends before settlement callbacks
// and excludes semantic parsing. Callbacks must tolerate concurrent invocations
// and must not retain ctx. The zero Scope still observes without accounting.
// Interrupted consumption takes precedence over an observed error, then consumer
// stop, then normal EOF. An observed error is cancelled when it has a context
// cancellation identity or matches the context cause at yield; otherwise it is
// failed. Later cleanup cancellation never reinterprets normal EOF or an error.
func (s Scope) InvokeObserved(ctx context.Context, call Call, source client, req llm.CompletionRequest, consume func(llm.CompletionStream), observed func(context.Context, Observation)) error {
	var record Observation
	present := false
	defer func() {
		if present {
			observed(ctx, record)
		}
	}()
	return s.invoke(ctx, call, func(account func(llm.CompletionStream) llm.CompletionStream) {
		present = true
		record = Observation{Operation: call.Operation, Provider: call.Provider, Model: call.Model, TaskID: taskscope.IDFrom(ctx), Depth: taskscope.DepthFrom(ctx)}
		started := time.Now()
		interrupted, stopped, finalized := true, false, false
		var streamErr error
		cancelled := false
		finalize := func() {
			if finalized {
				return
			}
			finalized = true
			record.Duration = time.Since(started)
			switch {
			case interrupted:
				record.Outcome = StreamOutcomes.INTERRUPTED
			case streamErr != nil:
				if cancelled {
					record.Outcome = StreamOutcomes.CANCELLED
				} else {
					record.Outcome = StreamOutcomes.FAILED
				}
			case stopped:
				record.Outcome = StreamOutcomes.STOPPED
			default:
				record.Outcome = StreamOutcomes.COMPLETED
			}
		}
		// This fallback must be inside admission: construction panic or a consumer
		// that never invokes the stream still finalizes before account settlement.
		defer finalize()
		stream := source.Complete(ctx, req)
		consume(account(func(yield func(llm.CompletionChunk, error) bool) {
			defer finalize()
			stream(func(chunk llm.CompletionChunk, err error) bool {
				if chunk.UsageReported {
					usage := chunk.Usage
					record.Usage = &usage
				}
				if chunk.PricingUsage != nil {
					usage := chunk.PricingUsage.Clone()
					record.PricingUsage = &usage
				}
				if chunk.Execution != nil {
					execution := chunk.Execution.Clone()
					record.Execution = &execution
				}
				if err != nil {
					streamErr = err
					cause := context.Cause(ctx)
					cancelled = errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (cause != nil && errors.Is(err, cause))
				}
				keep := yield(chunk, err)
				if !keep {
					stopped = true
				}
				return keep
			})
			interrupted = false
		}))
	})
}
