package program

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	"golang.org/x/sync/errgroup"
)

const (
	builtinCall     = "call"
	builtinCallMany = "call_many"
	builtinEmit     = "emit"
)

type nestedCall struct {
	Name     tools.ToolName
	Args     tools.ToolParameters
	Sequence int
}

type nestedResult struct {
	OK       bool                 `json:"ok"`
	Name     tools.ToolName       `json:"name,omitempty"`
	Args     tools.ToolParameters `json:"args,omitempty"`
	Sequence int                  `json:"sequence,omitempty"`
	Data     any                  `json:"data"`
	Error    string               `json:"error"`
}

type runner struct {
	ctx     context.Context
	cancel  context.CancelFunc
	source  *Source
	callID  tools.ToolCallID
	started time.Time
	nextSeq int
	thread  *starlark.Thread

	mu              sync.Mutex
	toolCalls       int
	parallelBatches int
	emitted         bool
	output          any
	scriptErr       *tools.Error
}

func newRunner(ctx context.Context, source *Source, callID tools.ToolCallID, started time.Time) *runner {
	ctx, cancel := context.WithTimeout(ctx, source.limits.Timeout)
	return &runner{ctx: ctx, cancel: cancel, source: source, callID: callID, started: started}
}

func (r *runner) run(script string) (any, Stats, *tools.Error) {
	defer r.cancel()
	if len(script) > r.source.limits.MaxScriptBytes {
		return nil, r.stats(), tools.Budget(op, fmt.Sprintf("script exceeds %d bytes", r.source.limits.MaxScriptBytes))
	}
	thread := &starlark.Thread{Name: "program", Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
		return nil, errors.New("load/import is disabled")
	}}
	thread.SetMaxExecutionSteps(r.source.limits.MaxExecutionSteps)
	r.thread = thread
	done := make(chan struct{})
	go func() {
		select {
		case <-r.ctx.Done():
			thread.Cancel(r.ctx.Err().Error())
		case <-done:
		}
	}()
	predeclared := starlark.StringDict{
		builtinCall:     newBuiltin(builtinCall, r.callBuiltin),
		builtinCallMany: newBuiltin(builtinCallMany, r.callManyBuiltin),
		builtinEmit:     newBuiltin(builtinEmit, r.emitBuiltin),
	}
	predeclared["true"] = starlark.True
	predeclared["false"] = starlark.False
	_, err := starlark.ExecFileOptions(&syntax.FileOptions{TopLevelControl: true}, thread, "program.star", script, predeclared)
	close(done)
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, r.stats(), contextError(r.ctx.Err())
		}
		if errObj := r.takeScriptError(); errObj != nil {
			return nil, r.stats(), errObj
		}
		return nil, r.stats(), tools.Validation(op, err.Error())
	}
	if r.ctx.Err() != nil {
		return nil, r.stats(), contextError(r.ctx.Err())
	}
	r.mu.Lock()
	emitted, output := r.emitted, r.output
	r.mu.Unlock()
	if !emitted {
		return nil, r.stats(), tools.Validation(op, "script did not emit a result")
	}
	return output, r.stats(), nil
}

func (r *runner) stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	steps := uint64(0)
	if r.thread != nil {
		steps = r.thread.ExecutionSteps()
	}
	return Stats{ToolCalls: r.toolCalls, ParallelBatches: r.parallelBatches, ExecutionSteps: steps, Duration: time.Since(r.started)}
}

func (r *runner) takeScriptError() *tools.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scriptErr
}

func (r *runner) setScriptError(errObj *tools.Error) *tools.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scriptErr = errObj
	return errObj
}

func (r *runner) reserveSequence() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	seq := r.nextSeq
	r.nextSeq++
	return seq
}

func (r *runner) reserveSequences(n int) []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	seqs := make([]int, n)
	for i := range seqs {
		seqs[i] = r.nextSeq
		r.nextSeq++
	}
	return seqs
}

func (r *runner) reserveCalls(n int) *tools.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.toolCalls+n > r.source.limits.MaxToolCalls {
		return r.setScriptError(tools.Budget(op, fmt.Sprintf("tool call budget exceeded: max %d", r.source.limits.MaxToolCalls)))
	}
	r.toolCalls += n
	return nil
}

func (r *runner) callBuiltin(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	var argDict *starlark.Dict
	if err := starlark.UnpackArgs(builtinCall, args, kwargs, "name", &name, "args?", &argDict); err != nil {
		return nil, err
	}
	params := tools.ToolParameters{}
	if argDict != nil {
		raw, err := fromStarlark(argDict)
		if err != nil {
			return nil, err
		}
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("args must be a string-keyed dictionary")
		}
		params = tools.ToolParameters(m)
	}
	nc := nestedCall{Name: tools.ToolName(name), Args: params, Sequence: r.reserveSequence()}
	if errObj := r.source.findAllowed(r.ctx, nc.Name); errObj != nil {
		return nil, r.setScriptError(errObj)
	}
	if errObj := r.reserveCalls(1); errObj != nil {
		return nil, errObj
	}
	res := r.executeNested(nc)
	return nestedResultValue(res)
}

func (r *runner) callManyBuiltin(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var list *starlark.List
	if err := starlark.UnpackArgs(builtinCallMany, args, kwargs, "calls", &list); err != nil {
		return nil, err
	}
	if list.Len() > r.source.limits.MaxParallelCalls {
		return nil, r.setScriptError(tools.Budget(op, fmt.Sprintf("parallel call batch exceeds %d", r.source.limits.MaxParallelCalls)))
	}
	calls := make([]nestedCall, 0, list.Len())
	for i := range list.Len() {
		raw, err := fromStarlark(list.Index(i))
		if err != nil {
			return nil, err
		}
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("call_many entry %d must be a dictionary", i)
		}
		name, ok := m["name"].(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("call_many entry %d requires string name", i)
		}
		params := tools.ToolParameters{}
		if rawArgs, ok := m["args"]; ok {
			am, ok := rawArgs.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("call_many entry %d args must be a dictionary", i)
			}
			params = tools.ToolParameters(am)
		}
		if errObj := r.source.findAllowed(r.ctx, tools.ToolName(name)); errObj != nil {
			return nil, r.setScriptError(errObj)
		}
		calls = append(calls, nestedCall{Name: tools.ToolName(name), Args: params})
	}
	seqs := r.reserveSequences(len(calls))
	for i := range calls {
		calls[i].Sequence = seqs[i]
	}
	if errObj := r.reserveCalls(len(calls)); errObj != nil {
		return nil, errObj
	}
	r.mu.Lock()
	r.parallelBatches++
	r.mu.Unlock()
	results := make([]nestedResult, len(calls))
	g, ctx := errgroup.WithContext(r.ctx)
	g.SetLimit(r.source.limits.MaxParallelCalls)
	for i, nc := range calls {
		g.Go(func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			results[i] = r.executeNested(nc)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, contextError(err)
	}

	vals := make([]starlark.Value, 0, len(results))
	for _, res := range results {
		v, err := nestedResultValue(res)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return starlark.NewList(vals), nil
}

func (r *runner) emitBuiltin(thread *starlark.Thread, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value starlark.Value
	if err := starlark.UnpackArgs(builtinEmit, args, kwargs, "value", &value); err != nil {
		return nil, err
	}
	raw, err := fromStarlark(value)
	if err != nil {
		return nil, err
	}
	out, err := normalizeJSON(raw, r.source.limits.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.emitted = true
	r.output = out
	r.mu.Unlock()
	return starlark.None, nil
}

func (r *runner) executeNested(nc nestedCall) nestedResult {
	call := tools.ToolCall{
		ID:        tools.ToolCallID(fmt.Sprintf("%s/%d", r.callID, nc.Sequence)),
		ToolName:  nc.Name,
		Arguments: nc.Args,
		Status:    tools.ToolCallStatusExecuting,
		CreatedAt: time.Now(),
	}
	started := time.Now()
	startedEvent := tools.NestedToolCall{ParentID: r.callID, ChildID: call.ID, Sequence: nc.Sequence, Call: call, Started: started}
	if obs := tools.NestedToolObserverFromContext(r.ctx); obs != nil {
		obs.OnNestedToolStarted(r.ctx, startedEvent)
	}
	var out nestedResult
	var result *tools.ToolResult
	var execErr error
	var kind tools.Kind
	defer func() {
		if obs := tools.NestedToolObserverFromContext(r.ctx); obs != nil {
			obs.OnNestedToolFinished(r.ctx, tools.NestedToolResult{NestedToolCall: startedEvent, Result: result, Err: execErr, Kind: kind, Error: out.Error, Duration: time.Since(started)})
		}
	}()
	if errObj := r.source.findAllowed(r.ctx, nc.Name); errObj != nil {
		execErr = errObj
		kind = errObj.Kind
		out = nestedFailure(nc, errObj.Error())
		return out
	}
	res, err := r.source.inner.Execute(r.ctx, call)
	result, execErr = res, err
	if err != nil {
		out = nestedFailure(nc, err.Error())
		return out
	}
	if res == nil {
		out = nestedFailure(nc, "tool returned nil result")
		return out
	}
	if res.Err != nil {
		kind = res.Err.Kind
	}
	if len(res.Effects) > 0 {
		out = nestedFailure(nc, "nested tool produced effects")
		return out
	}
	if !res.Success {
		if res.Error != "" {
			out = nestedFailure(nc, res.Error)
			return out
		}
		out = nestedFailure(nc, "tool call failed")
		return out
	}
	data, err := normalizeJSON(res.Data, r.source.limits.MaxToolResultBytes)
	if err != nil {
		execErr = err
		out = nestedFailure(nc, err.Error())
		return out
	}
	out = nestedSuccess(nc, data)
	return out
}

func nestedSuccess(call nestedCall, data any) nestedResult {
	return nestedResult{OK: true, Name: call.Name, Args: call.Args, Sequence: call.Sequence, Data: data}
}

func nestedFailure(call nestedCall, msg string) nestedResult {
	return nestedResult{OK: false, Name: call.Name, Args: call.Args, Sequence: call.Sequence, Error: msg}
}

func nestedResultValue(res nestedResult) (starlark.Value, error) {
	m := map[string]any{"ok": res.OK, "data": res.Data, "error": res.Error}
	if res.Name != "" {
		m["name"] = res.Name.String()
	}
	if len(res.Args) > 0 {
		m["args"] = map[string]any(res.Args)
	}
	return toStarlark(m)
}
