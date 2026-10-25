package engine

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

const controlPolicyBuild = "build"

type requestControlSink interface {
	RequestControlRecorded(context.Context, runner.ReplayControl)
}

func (l *LiveRunner) requestControlCapture(ctx context.Context) (
	requestControlObservation,
	requestControlObservation,
	func(runner.ReplayControl),
) {
	l.mu.Lock()
	rendered := l.renderedControl
	current := requestControlObservation{policyLabel: controlPolicyBuild, generation: l.modeGeneration}
	if l.target.Plan || l.readOnly {
		current.policyLabel = modePlan
	}
	sink := l.sink
	l.mu.Unlock()

	observer, ok := sink.(requestControlSink)
	if !ok {
		return rendered, current, nil
	}
	return rendered, current, func(control runner.ReplayControl) {
		observer.RequestControlRecorded(ctx, control)
	}
}
