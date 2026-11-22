package engine

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/options"
)

// recordingOptions binds attribution to the actual resolved construction target,
// not the separately snapshotted legacy run header. Descendant MAIN sinks
// share this run-owned recorder; auxiliary streams use their own factory.
func (r *headlessRecorder) recordingOptions(downstream runner.EventSink, root taskscope.ID) turnOptionsFactory {
	if r == nil {
		return nil
	}
	return func(target RunTarget) turnRecordingOptions {
		return turnRecordingOptions{root: []options.Option[runner.Runner]{
			runner.WithSink(headlessAccountingSink{
				EventSink: downstream, recorder: r, root: root,
				provider: target.Spec.Name, model: target.Model,
			}),
		}, child: func(child RunTarget) runner.EventSink {
			return headlessAccountingSink{
				EventSink: downstream, recorder: r, root: root,
				provider: child.Spec.Name, model: child.Model,
			}
		}, auxiliary: newAuxiliaryProviderFactory(r.settleAuxiliary)}
	}
}

type headlessAccountingSink struct {
	runner.EventSink
	recorder        *headlessRecorder
	root            taskscope.ID
	provider, model string
}

func (s headlessAccountingSink) OnProviderAttemptSettled(ctx context.Context, event runner.ProviderAttemptSettled) {
	// Fallback children may reuse the root runner, including its sink.
	if event.Depth == 0 && event.TaskID == s.root {
		s.recorder.settle(event, s.provider, s.model)
	} else if event.Depth > 0 {
		s.recorder.settleChild(event, s.provider, s.model)
	}
	s.EventSink.OnProviderAttemptSettled(ctx, event)
}

func (r *headlessRecorder) settle(event runner.ProviderAttemptSettled, provider, model string) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if !r.lease.active {
		r.rejectCaptureLocked()
		return
	}
	record := RecordedHeadlessProviderAccounting{
		RunID: r.id, PursueAttempt: r.lease.number, ProviderOrdinal: event.Attempt,
		Provider: provider, Model: model,
		Usage: event.Usage, PricingUsage: event.PricingUsage, RateCard: event.RateCard,
	}
	if event.Execution != nil {
		cost := event.Execution.ReportedCost
		cost.Source = ""
		record.ReportedCost = &cost
	}
	record = record.Clone()
	if record.RateCard != nil && record.PricingUsage != nil {
		estimate := backends.EstimatePrice(*record.RateCard, *record.PricingUsage)
		record.ConfiguredEstimate = &estimate
	}
	data, err := EncodeRecordedHeadlessProviderAccounting(record)
	if err != nil {
		r.rejectCaptureLocked()
		return
	}
	r.pending = append(r.pending, db.HeadlessProviderAccounting{
		PursueAttempt: record.PursueAttempt, ProviderOrdinal: record.ProviderOrdinal,
		Version: 1, PayloadJSON: data,
	})
}

func (r *headlessRecorder) rejectCaptureLocked() {
	if r.captureErr == nil {
		// A safe semantic identity, never an event, execution metadata or raw error.
		r.captureErr = ErrInvalidRecordedHeadlessProviderAccounting
	}
}

// ownedAttempt counts entry into EVERY actual Run, including zero-provider,
// cancelled and panicking invocations. Reports never advance or release a lease.
func (r *headlessRecorder) ownedAttempt(run pursue.AttemptFunc) pursue.AttemptFunc {
	if r == nil {
		return run
	}
	return func(ctx context.Context, spec runner.TaskSpec) runner.TaskResult {
		r.stateMu.Lock()
		number := r.nextAttempt
		r.nextAttempt++
		if r.lease.active {
			r.rejectCaptureLocked()
		} else {
			r.lease = headlessAttemptLease{number: number, active: true}
		}
		r.stateMu.Unlock()
		defer func() {
			r.stateMu.Lock()
			if r.lease.number == number {
				r.lease.active = false
			}
			r.stateMu.Unlock()
		}()
		return run(ctx, spec)
	}
}

func (r *headlessRecorder) ownedReport(ctx context.Context, report pursue.AttemptReport) {
	r.stateMu.Lock()
	// A synthetic report can precede even ENTRY of an admitted goroutine.
	// Launch rejection likewise has a summary but no provider occurrence.
	if report.Attempt.Number != r.lease.number && report.Attempt.Number != r.nextAttempt {
		r.rejectCaptureLocked()
	}
	r.stateMu.Unlock()
	r.attempt(ctx, report)
}
