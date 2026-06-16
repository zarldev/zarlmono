package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// dispatchedCall is the result of a single tool dispatch, keyed by
// ToolCallID inside dispatchBatch's return map.
type dispatchedCall struct {
	result *tools.ToolResult
	err    error
}

// dispatchBatch runs every tool call in toolCallOrder through the
// registry. Calls run in parallel up to r.toolConcurrency via an
// errgroup with SetLimit; their results are returned keyed by the
// LLM's call ID so the caller can stitch them back into the original
// order. A toolConcurrency value of 0 or 1 falls through to a fully
// sequential dispatch.
//
// The errgroup never returns an error to the runner: tool failures
// are surfaced as a non-nil err on the dispatchedCall struct (and as
// ToolExecutionFailed events) so the runner can append them as tool
// messages and let the model recover rather than aborting the iteration.
func (r *Runner) dispatchBatch(
	ctx context.Context,
	spec TaskSpec,
	toolCalls map[string]*llm.ToolCall,
	toolCallOrder []string,
) map[string]dispatchedCall {
	out := make(map[string]dispatchedCall, len(toolCallOrder))
	limit := max(r.toolConcurrency, 1)
	if limit == 1 || len(toolCallOrder) <= 1 {
		for _, id := range toolCallOrder {
			tc := toolCalls[id]
			res, err := r.dispatch(ctx, spec, tc)
			out[id] = dispatchedCall{result: res, err: err}
		}
		return out
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	var mu sync.Mutex // guards the result map
	for _, id := range toolCallOrder {
		tc := toolCalls[id]
		g.Go(func() error {
			res, err := r.dispatch(gctx, spec, tc)
			mu.Lock()
			out[id] = dispatchedCall{result: res, err: err}
			mu.Unlock()
			// Returning an error would cancel the errgroup's context
			// and short-circuit siblings. Tool failures are tracked
			// per-call and shouldn't cancel the batch — surface them
			// via the dispatchedCall struct instead.
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// dispatch routes a tool call through the Registry. Publishes
// ToolExecutionStarted / Completed / Failed events on the way through.
func (r *Runner) dispatch(
	ctx context.Context,
	spec TaskSpec,
	tc *llm.ToolCall,
) (*tools.ToolResult, error) {
	name := tools.ToolName(tc.Function.Name)
	args := tools.ToolParameters{}
	// repair.Unmarshal accepts an empty buffer (decodes as `{}`) and
	// tries a cascade of small-model recovery transforms on malformed
	// input — literal newlines in strings, trailing commas, missing
	// closers — before giving up. On total failure we fail the call
	// with a Validation result so the model gets a clear "your JSON
	// didn't parse" message rather than a tool-side "X required"
	// once dispatch lands with empty args.
	//
	// Build the typed *tools.Error first and project it via
	// failedFromError so the resulting Result carries Kind structurally
	// (errors.AsType extracts it) — same pattern as code.failure and
	// failedFromGuard, with no duplication of the projection logic.
	if err := repair.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return tools.Failure(tc.ID, tools.Validation(string(name), fmt.Sprintf(
			"tool arguments did not parse as JSON (after repair attempts): %v. "+
				"Re-emit the call with valid JSON. Common fixes: escape literal newlines "+
				"as \\n inside string values, remove trailing commas, double-quote keys.",
			err))), nil
	}
	call := tools.ToolCall{
		ID:        tc.ID,
		ToolName:  name,
		Arguments: args,
		Status:    tools.ToolCallStatusExecuting,
		CreatedAt: time.Now(),
	}

	r.publishToolStarted(ctx, spec, call)
	// Gate check: need the tool spec to evaluate capability-based policy.
	// The tool should already be hidden from the LLM list by buildLLMTools;
	// this is a backstop for calls from memory or tool name hallucinations.
	if gate := toolGateFrom(ctx); gate != nil {
		// Resolve the authoritative spec from the same snapshot buildLLMTools
		// ships to the LLM. FAIL CLOSED when it can't be resolved: a wrapper
		// source (guarded / composite / sourcechain) or a hallucinated name
		// yields no spec, and evaluating the gate against a zero-value spec
		// would read as Mutates==false / Name=="" and silently slip an
		// explore/verify gate. An unresolvable tool is refused, not admitted.
		toolSpec, found := r.specForGate(ctx, name)
		if !found || !gate(toolSpec) {
			res := tools.Failure(call.ID, tools.Validation(string(name), fmt.Sprintf(
				"%q is not available to this sub-agent in its current work mode", name)))
			r.publishToolFinished(ctx, spec, call, res, 0, nil, false)
			return res, nil
		}
	}
	startTS := time.Now()
	execCtx := ctx
	if r.timeouts.tool > 0 {
		// Apply the per-tool budget. A well-behaved tool sees
		// ctx.Done() and unwinds; one that ignores ctx keeps running
		// in its own goroutine past the deadline, but the runner stops
		// waiting and reports a timeout result so subsequent iterations
		// aren't blocked by a wedged dispatch.
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.timeouts.tool)
		defer cancel()
	}
	type toolExecResult struct {
		result *tools.ToolResult
		err    error
	}
	done := make(chan toolExecResult, 1)
	go func() {
		// A panicking tool — Execute nil deref, a buggy MCP server, a bad
		// dynamic tool — must not take the whole runner down. Recover here
		// (this is the goroutine the arbitrary tool code actually runs in,
		// not the errgroup goroutine in dispatchBatch) and turn the panic
		// into a Transient failure so the model gets a clear signal and the
		// loop survives. The buffered channel means this send never blocks,
		// even if the select already moved on via the per-tool deadline.
		defer func() {
			if rec := recover(); rec != nil {
				slog.ErrorContext(execCtx, "tool execution panicked; recovered",
					"tool", string(name),
					"panic", fmt.Sprintf("%v", rec),
					"stack", string(debug.Stack()))
				done <- toolExecResult{result: tools.Failure(call.ID, tools.Transient(string(name), fmt.Errorf(
					"tool %q panicked during execution: %v; the dispatch was abandoned. "+
						"This is a bug in the tool itself, not your arguments — try a different "+
						"tool or approach rather than re-issuing the same call",
					name, rec)))}
			}
		}()
		result, err := r.tools.Execute(execCtx, call)
		done <- toolExecResult{result: result, err: err}
	}()

	var result *tools.ToolResult
	var err error
	// inFlight is true when we stopped waiting via the per-tool deadline
	// rather than the tool returning — i.e. its goroutine is still running.
	var inFlight bool
	select {
	case out := <-done:
		result, err = out.result, out.err
	case <-execCtx.Done():
		err = execCtx.Err()
		inFlight = true
	}
	// If the tool returned because of our per-tool deadline (and not
	// the caller's outer ctx), surface a Timeout-classed failure so
	// the model sees a clear "tool exceeded its time budget" signal
	// rather than a generic "context deadline exceeded" string.
	abandoned := false
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && r.timeouts.tool > 0 {
		// Abandoned only when the tool didn't stop on the deadline — its
		// goroutine is still in flight and may keep mutating state.
		abandoned = inFlight
		err = nil
		result = tools.Failure(call.ID, tools.Transient(string(name), fmt.Errorf(
			"tool %q exceeded the per-tool time budget (%s); the dispatch was abandoned. "+
				"If the work is legitimately long-running, split it across multiple calls "+
				"or run it as a background bash with `background: true`",
			name, r.timeouts.tool)))
	}
	r.publishToolFinished(ctx, spec, call, result, time.Since(startTS), err, abandoned)
	return result, err
}

// toolMutates reports whether the named tool declares Mutates in its
// spec — the CompletionGate's "this call changed durable state" signal.
// Resolves through the same snapshot as the dispatch gate so wrapper
// sources (guarded / composite) still see the real spec; an unresolvable
// name reads as non-mutating, which is the safe default (it cannot make
// the gate believe work happened that didn't).
func (r *Runner) toolMutates(ctx context.Context, name string) bool {
	spec, ok := r.specForGate(ctx, tools.ToolName(name))
	return ok && spec.Mutates
}

// specForGate resolves a tool's authoritative spec for the dispatch gate.
// It scans the SAME snapshot buildLLMTools ships to the LLM
// (r.tools.Tools(ctx)) rather than a registry-only Tool(name) lookup, so a
// wrapper source (guarded / composite / sourcechain) — which may not expose
// a direct lookup — still resolves the real spec instead of falling through
// to a zero value. found is false only when no tool of that name is exposed
// by the source; callers fail closed on that.
func (r *Runner) specForGate(ctx context.Context, name tools.ToolName) (tools.ToolSpec, bool) {
	for t := range r.tools.Tools(ctx) {
		if s := t.Definition(); s.Name == name {
			return s, true
		}
	}
	return tools.ToolSpec{}, false
}

// buildLLMTools snapshots the registry's current tool list as the
// per-iteration tool set shipped to the LLM. Called once per iteration
// so newly-registered tools (the agent built one with `register`,
// MCP just connected, etc.) become callable on the next turn without
// needing a runner restart.
func (r *Runner) buildLLMTools(ctx context.Context) []llm.Tool {
	gate := toolGateFrom(ctx)
	var out []llm.Tool
	for t := range r.tools.Tools(ctx) {
		s := t.Definition()
		if gate != nil && !gate(s) {
			continue // hidden from this (gated) Run's tool surface
		}
		out = append(out, llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        s.Name.String(),
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		})
	}
	return out
}
