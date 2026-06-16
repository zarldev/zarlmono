package guardrails

import (
	"context"
	"fmt"
	"iter"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Source is the tool source contract a GuardedSource wraps — an alias of
// [tools.Source] so guardrail consumers don't import the tools package
// just to name the seam.
type Source = tools.Source

func successfulResult(result *tools.ToolResult, err error) bool {
	return err == nil && result != nil && result.Success
}

// Guardrail is the base contract every guardrail satisfies — it has
// an identifier surfaced in errors and logs. Most concrete guardrails
// also satisfy PreCall, PostCall, or both; GuardedSource dispatches
// to whichever phase the guardrail opts into.
//
// Guardrails may be invoked concurrently by a single GuardedSource because the
// runner dispatches tool calls in parallel. Any guardrail with mutable state
// must synchronize its own Before/Inspect/ForgetTask methods; GuardedSource
// does not serialize calls.
type Guardrail interface {
	Name() string
}

// PreCall is the optional pre-execution check. A guardrail satisfying
// PreCall runs before the underlying tool executes; returning a
// non-nil error short-circuits dispatch and converts the call into a
// failed ToolResult.
//
// Use for: schema validation, argument allowlists, repeat-call caps,
// budget pre-checks — anything that can decide "don't run this" from
// (call, ctx) alone.
type PreCall interface {
	Guardrail
	Before(ctx context.Context, call tools.ToolCall) error
}

// PostCall is the optional post-execution check. A guardrail
// satisfying PostCall runs after the tool executes; returning a
// non-nil error replaces the result with a failed ToolResult.
//
// Use for: output-shape validation, content checks, determinism
// cross-checks, transient-error reclassification — anything that
// needs to see (result, execErr) to decide.
type PostCall interface {
	Guardrail
	Inspect(ctx context.Context, call tools.ToolCall, result *tools.ToolResult, execErr error) error
}

// GuardedSource wraps a ToolSource and runs each guardrail at the
// phases it satisfies. PreCall guardrails run first, in order; the
// first rejection short-circuits dispatch. PostCall guardrails run
// after the tool executes, in order; the first rejection short-
// circuits the rest.
//
// Rejections always produce a failed ToolResult, never a hard error
// out of Execute — the runner appends the failed result to the
// conversation so the model can react.
//
// When a rejection carries a *tools.Error (e.g. tools.Validation),
// the resulting ToolResult.Kind is stamped with the typed Kind so
// downstream consumers can classify without parsing strings.
type GuardedSource struct {
	inner  Source
	guards []Guardrail
}

// NewGuardedSource wraps source with the given guardrails. Zero
// guardrails is a valid pass-through.
func NewGuardedSource(source Source, guards ...Guardrail) *GuardedSource {
	return &GuardedSource{inner: source, guards: guards}
}

// Tools delegates to the inner source. Guardrails don't change which
// tools the LLM sees — only what happens around dispatch.
func (g *GuardedSource) Tools(ctx context.Context) iter.Seq[tools.Tool] {
	return g.inner.Tools(ctx)
}

// Names returns the identifier of every wrapped guardrail in
// installation order. Exposed so observability surfaces (the
// context pane's "guardrails" strip) can paint which checks are
// armed for the current task. Returns an empty slice when no
// guardrails are wired.
func (g *GuardedSource) Names() []string {
	out := make([]string, 0, len(g.guards))
	for _, guard := range g.guards {
		out = append(out, guard.Name())
	}
	return out
}

// ForgetTask forwards task lifecycle cleanup to guardrails that keep per-task
// state AND to the wrapped source, so cleanup reaches every layer of a
// wrapper chain (mirrors MemoSource.ForgetTask, which forwards inward the
// same way). Layers without state are ignored.
func (g *GuardedSource) ForgetTask(id taskscope.ID) {
	for _, guard := range g.guards {
		if tf, ok := guard.(interface{ ForgetTask(taskscope.ID) }); ok {
			tf.ForgetTask(id)
		}
	}
	if tf, ok := g.inner.(interface{ ForgetTask(taskscope.ID) }); ok {
		tf.ForgetTask(id)
	}
}

// Execute runs pre-call guardrails, dispatches the call through the
// inner source, then runs post-call guardrails. A rejection at either
// phase produces a failed ToolResult; hard errors from the inner
// source pass through unchanged unless a post-call guardrail rejects.
func (g *GuardedSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	for _, guard := range g.guards {
		pre, ok := guard.(PreCall)
		if !ok {
			continue
		}
		if perr := pre.Before(ctx, call); perr != nil {
			return failedFromGuard(call, guard.Name(), perr), nil
		}
	}
	result, err := g.inner.Execute(ctx, call)
	for _, guard := range g.guards {
		post, ok := guard.(PostCall)
		if !ok {
			continue
		}
		if gerr := post.Inspect(ctx, call, result, err); gerr != nil {
			return failedFromGuard(call, guard.Name(), gerr), nil
		}
	}
	return result, err
}

// failedFromGuard packages a guardrail rejection as a failed
// ToolResult. The Kind field is populated when the rejection carries
// (or wraps) a *tools.Error so schema-style rejections classify as
// Validation, budget rejections as Budget, etc.
func failedFromGuard(call tools.ToolCall, name string, err error) *tools.ToolResult {
	res := tools.Failure(call.ID, err)
	// Re-prefix the projected string with the guardrail name so log
	// readers can tell guardrail rejections apart from tool-side
	// failures. The structural classification (Kind) is unchanged.
	res.Error = fmt.Sprintf("guardrail %q: %v", name, err)
	return res
}
