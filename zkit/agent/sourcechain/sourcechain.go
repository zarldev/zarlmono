// Package sourcechain composes reusable tool-source wrappers with the
// production guardrail stack. It deliberately has no dependency on runner:
// callers decide whether to wrap the returned source in runner.MemoSource.
package sourcechain

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Wrapper transforms one tool source into another.
type Wrapper func(tools.Source) tools.Source

// Pipeline applies source wrappers in a fixed semantic order regardless of the
// order options were supplied.
type Pipeline struct {
	diffRecorder Wrapper
	formatSwitch Wrapper
	modeFilter   Wrapper
	depthFilter  Wrapper
}

// PipelineOption configures a Pipeline slot.
type PipelineOption func(*Pipeline)

// NewPipeline returns a pipeline with the supplied wrapper slots. Unset slots
// are identity/pass-through.
func NewPipeline(opts ...PipelineOption) Pipeline {
	var p Pipeline
	for _, opt := range opts {
		if opt != nil {
			opt(&p)
		}
	}
	return p
}

// WithDiffRecorder fills the diff-recorder slot — the wrapper that snapshots
// file state around mutating calls. It runs first, closest to the base source.
func WithDiffRecorder(w Wrapper) PipelineOption { return func(p *Pipeline) { p.diffRecorder = w } }

// WithFormatSwitch fills the format-switch slot — the wrapper that rewrites
// tool output format arguments (labelled vs JSON) per consumer preference.
func WithFormatSwitch(w Wrapper) PipelineOption { return func(p *Pipeline) { p.formatSwitch = w } }

// WithModeFilter fills the mode-filter slot — the wrapper that hides tools
// based on the session's plan/build mode.
func WithModeFilter(w Wrapper) PipelineOption { return func(p *Pipeline) { p.modeFilter = w } }

// WithDepthFilter fills the depth-filter slot — the wrapper that trims the
// tool surface for sub-agent runs by recursion depth. It runs last.
func WithDepthFilter(w Wrapper) PipelineOption { return func(p *Pipeline) { p.depthFilter = w } }

// Wrap applies wrappers in fixed order: diff recorder → format switch → mode
// filter → depth filter. Nil wrappers are identity.
func (p Pipeline) Wrap(base tools.Source) tools.Source {
	src := base
	for _, w := range []Wrapper{p.diffRecorder, p.formatSwitch, p.modeFilter, p.depthFilter} {
		if w != nil {
			src = w(src)
		}
	}
	return src
}

// Chain is the armed source plus a snapshot of guardrail names for
// observability surfaces.
type Chain struct {
	Source         tools.Source
	GuardrailNames []string
}

type config struct {
	extra []guardrails.Guardrail
}

// Option configures guardrail arming.
type Option func(*config)

// WithExtraGuardrails appends truly-extra guardrails after the production set.
func WithExtraGuardrails(extra ...guardrails.Guardrail) Option {
	return func(c *config) { c.extra = append(c.extra, extra...) }
}

// New arms schema + production guardrails over an already-wrapped source.
func New(wrapped tools.Source, deps guardrails.Deps, opts ...Option) (*Chain, error) {
	var cfg config
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	guards := []guardrails.Guardrail{guardrails.NewSchemaGuardrail(wrapped)}
	// Plan-first gates on each tool's ChangesWorkspace capability, so it needs
	// the same spec Iterable schema uses — compose it here, right after schema,
	// rather than in PostSchemaGuardrails (which has only deps). It runs before
	// the policy guardrails so a changing call is refused for "no plan" ahead of
	// any scope/fan-out advice.
	if deps.PlanFirst && deps.PlanTool != "" {
		guards = append(guards, guardrails.NewPlanGuardrail(wrapped, deps.PlanTool))
	}
	guards = append(guards, guardrails.PostSchemaGuardrails(deps)...)
	guards = append(guards, cfg.extra...)

	seen := map[string]struct{}{}
	for i, guard := range guards {
		if guard == nil {
			return nil, fmt.Errorf("guardrail %d is nil", i)
		}
		name := guard.Name()
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate guardrail name %q", name)
		}
		seen[name] = struct{}{}
	}

	guarded := guardrails.NewGuardedSource(wrapped, guards...)
	return &Chain{Source: guarded, GuardrailNames: guarded.Names()}, nil
}
