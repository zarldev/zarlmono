package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	result      *tools.ToolResult
	rawResult   *tools.ToolResult // Settled execution output, even after cancellation.
	parameters  tools.ToolParameters
	err         error
	executionID string
	dispatched  bool
}

func allocateExecutionID() string {
	return "execution-" + rand.Text()
}

// dispatchBatch runs every tool call in toolCallOrder through the
// registry. By default, only consecutive workspace reads overlap; every other
// call is an ordered barrier. Explicit WithToolConcurrency opts into unrestricted
// batches (or sequential dispatch). Results retain their original call keys so
// the caller can append them in model order, regardless of completion order.
//
// The errgroup never returns an error to the runner: tool failures
// are surfaced as a non-nil err on the dispatchedCall struct (and as
// ToolExecutionFailed events) so the runner can append them as tool
// messages and let the model recover rather than aborting the iteration.
func (t *taskRun) dispatchBatch(
	ctx context.Context,
	toolCalls map[string]llm.ToolCall,
	toolCallOrder []string,
) map[string]dispatchedCall {
	r := t.r
	out := make(map[string]dispatchedCall, len(toolCallOrder))
	limit := max(r.toolConcurrency, 1)
	if limit == 1 {
		t.dispatchCalls(ctx, toolCalls, toolCallOrder, out, limit)
		return out
	}
	for len(toolCallOrder) > 0 {
		n := 1
		if r.parallelCall(ctx, toolCalls[toolCallOrder[0]]) {
			for n < len(toolCallOrder) && r.parallelCall(ctx, toolCalls[toolCallOrder[n]]) {
				n++
			}
		}
		// Join each group before resolving the next one: a barrier can change
		// both workspace state and the tool registry itself.
		t.dispatchCalls(ctx, toolCalls, toolCallOrder[:n], out, limit)
		toolCallOrder = toolCallOrder[n:]
	}
	return out
}

func (r *Runner) parallelCall(ctx context.Context, call llm.ToolCall) bool {
	spec, found := r.specForGate(ctx, tools.ToolName(call.Function.Name))
	if !found {
		return !r.parallelReadsOnly
	}
	return !spec.DispatchBarrier && (!r.parallelReadsOnly || spec.Access() == tools.WorkspaceAccesses.READ && !spec.ChangesWorkspace())
}

// dispatchCalls joins all admitted work before returning. Cancellation is
// checked at executeTool, so queued calls still settle without executing.
func (t *taskRun) dispatchCalls(ctx context.Context, toolCalls map[string]llm.ToolCall, toolCallOrder []string, out map[string]dispatchedCall, limit int) {
	if limit == 1 || len(toolCallOrder) <= 1 {
		for _, id := range toolCallOrder {
			tc := toolCalls[id]
			executionID := allocateExecutionID()
			res, raw, parameters, didDispatch, err := t.dispatch(ctx, tc, executionID)
			out[id] = dispatchedCall{result: res, rawResult: raw, parameters: parameters, err: err, executionID: executionID, dispatched: didDispatch}
		}
		return
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	var mu sync.Mutex // guards the result map
	for _, id := range toolCallOrder {
		tc := toolCalls[id]
		executionID := allocateExecutionID()
		g.Go(func() error {
			res, raw, parameters, didDispatch, err := t.dispatch(gctx, tc, executionID)
			mu.Lock()
			out[id] = dispatchedCall{result: res, rawResult: raw, parameters: parameters, err: err, executionID: executionID, dispatched: didDispatch}
			mu.Unlock()
			// Returning an error would cancel the errgroup's context
			// and short-circuit siblings. Tool failures are tracked
			// per-call and shouldn't cancel the batch — surface them
			// via the dispatchedCall struct instead.
			return nil
		})
	}
	_ = g.Wait()
}

// dispatch routes a tool call through the Registry. Publishes
// ToolExecutionStarted / Completed / Failed events on the way through.
func (t *taskRun) dispatch(
	ctx context.Context,
	tc llm.ToolCall,
	executionID string,
) (*tools.ToolResult, *tools.ToolResult, tools.ToolParameters, bool, error) {
	r, spec := t.r, t.spec
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
	call := tools.ToolCall{
		ID: tools.ToolCallID(tc.ID), ExecutionID: executionID, ToolName: name,
		Arguments: args, RawArguments: tc.Function.Arguments,
		Status: tools.ToolCallStatusExecuting, CreatedAt: time.Now(),
	}
	if err := repair.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		r.publishToolStarted(ctx, spec, call)
		res := tools.Failure(call.ID, tools.Validation(string(name), fmt.Sprintf(
			"tool arguments did not parse as JSON (after repair attempts): %v. Re-emit the call with valid JSON", err)))
		r.publishToolFinished(ctx, spec, call, res, res, 0, nil)
		return res, res, nil, false, nil
	}
	parameters := tools.CloneParameters(args)
	call.Arguments = args
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
			r.publishToolFinished(ctx, spec, call, res, res, 0, nil)
			return res, res, parameters, false, nil
		}
	}
	startTS := time.Now()
	execCtx := ctx
	toolTimeout := r.toolTimeout(name)
	if toolTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, toolTimeout)
		defer cancel()
	}
	nested := newNestedToolPublisher(r, spec, call.ExecutionID)
	nested.attempt, nested.capture = t.attempt, t.recordExecution
	execCtx = tools.ContextWithNestedToolObserver(execCtx, nested)
	execCtx = tools.ContextWithWorkspaceWaitObserver(execCtx, workspaceWaitPublisher{r: r, spec: spec, call: call, nested: nested})
	execCtx = tools.ContextWithWorkspaceWaitCall(execCtx, tools.WorkspaceWaitCall{ToolID: call.ID, ToolName: call.ToolName})
	// Execute synchronously: the batch owns this work until it actually exits.
	// Cancellation requests a stop; it cannot forcibly terminate arbitrary Go code.
	raw, didDispatch, err := r.executeTool(execCtx, call)
	if raw == nil && err != nil {
		raw = tools.Failure(call.ID, err)
	}
	result := raw
	if cause := execCtx.Err(); cause != nil {
		err = cause
		result = tools.Failure(call.ID, tools.Transient(string(name), cause))
		if errors.Is(cause, context.DeadlineExceeded) && ctx.Err() == nil && toolTimeout > 0 {
			result = tools.Failure(call.ID, tools.Transient(string(name), fmt.Errorf(
				"tool %q exceeded the per-tool time budget (%s); execution has now stopped: %w", name, toolTimeout, cause)))
		}
	}
	r.publishToolFinished(ctx, spec, call, result, raw, time.Since(startTS), err)
	return result, raw, parameters, didDispatch, errors.Join(err, nested.historyError())
}

func (r *Runner) executeTool(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, bool, error) {
	var dispatched bool
	var result *tools.ToolResult
	var execErr error
	func() {
		defer func() {
			if value := recover(); value != nil {
				result = tools.Failure(call.ID, tools.Transient(string(call.ToolName), fmt.Errorf(
					"tool %q panicked during execution: %v", call.ToolName, value)))
			}
		}()
		if err := ctx.Err(); err != nil {
			execErr = err
			return
		}
		dispatched = true
		result, execErr = r.tools.Execute(ctx, call)
	}()
	return result, dispatched, execErr
}

// recordUndispatchedCalls retains partial attempts without admitting execution.
func (t *taskRun) recordUndispatchedCalls(ctx context.Context, calls map[string]*llm.ToolCall, order []string, cause error) ([]ToolOutput, error) {
	r, spec := t.r, t.spec
	var outputs []ToolOutput
	var captureErr error
	for _, id := range order {
		tc := calls[id]
		call := tools.ToolCall{
			ID: tools.ToolCallID(tc.ID), ExecutionID: allocateExecutionID(),
			ToolName: tools.ToolName(tc.Function.Name), RawArguments: tc.Function.Arguments,
			Status: tools.ToolCallStatusExecuting, CreatedAt: time.Now(),
		}
		result := tools.Failure(call.ID, tools.Transient(tc.Function.Name, fmt.Errorf("completion interrupted before tool dispatch: %w", cause)))
		r.publishToolStarted(ctx, spec, call)
		r.publishToolFinished(ctx, spec, call, result, result, 0, cause)
		success, terminalError, kind := classifyToolOutput(result)
		output := ToolOutput{ExecutionID: call.ExecutionID, ToolCallID: tc.ID, ToolName: tc.Function.Name,
			TaskID: string(spec.ID), Attempt: t.attempt, Dispatched: new(false),
			Args: tc.Function.Arguments, Output: rawToolResultText(result), Success: success, Error: terminalError, Kind: kind}
		outputs = append(outputs, output)
		if r.toolOutputSink != nil {
			err := r.toolOutputSink.Record(ctx, output)
			if err != nil {
				captureErr = errors.Join(captureErr, fmt.Errorf("%w: %w", ErrToolHistory, err))
			}
		}
	}
	return outputs, captureErr
}

func (r *Runner) toolTimeout(tools.ToolName) time.Duration {
	return r.timeouts.tool
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

type requestTools struct {
	tools   []llm.Tool
	surface ToolSurface
}

type diagnosticTool struct {
	Tool             llm.Tool `json:"tool"`
	Mutates          bool     `json:"mutates"`
	AffectsWorkspace bool     `json:"affects_workspace"`
}

// buildRequestTools takes one post-gate snapshot and derives both the provider
// payload and its accounting from it. Keeping those outputs under one owner
// prevents observability from measuring a different surface than the request.
func (r *Runner) buildRequestTools(ctx context.Context, previousFingerprint string) (requestTools, error) {
	gate := toolGateFrom(ctx)
	var out requestTools
	var diagnostic []diagnosticTool
	seen := make(map[tools.ToolName]struct{})
	for tool := range r.tools.Tools(ctx) {
		spec := tool.Definition()
		if gate != nil && !gate(spec) {
			continue
		}
		if err := tools.ValidateToolSpec(spec); err != nil {
			continue
		}
		if _, ok := seen[spec.Name]; ok {
			return requestTools{}, fmt.Errorf("build request tools: duplicate tool name %q", spec.Name)
		}
		seen[spec.Name] = struct{}{}

		modelTool := llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        spec.Name.String(),
				Description: spec.Description,
				Parameters:  spec.Parameters,
			},
		}
		out.tools = append(out.tools, modelTool)
		diagnostic = append(diagnostic, diagnosticTool{
			Tool:             modelTool,
			Mutates:          spec.Mutates,
			AffectsWorkspace: spec.AffectsWorkspace,
		})
	}

	wireJSON, err := json.Marshal(out.tools)
	if err != nil {
		return requestTools{}, fmt.Errorf("serialize request tools: %w", err)
	}
	diagnosticJSON, err := json.Marshal(diagnostic)
	if err != nil {
		return requestTools{}, fmt.Errorf("serialize diagnostic tools: %w", err)
	}
	digest := sha256.Sum256(diagnosticJSON)
	fingerprint := hex.EncodeToString(digest[:])
	out.surface = ToolSurface{
		Count:       len(out.tools),
		JSONBytes:   len(wireJSON),
		Fingerprint: fingerprint,
		Changed:     previousFingerprint != "" && previousFingerprint != fingerprint,
	}
	return out, nil
}
