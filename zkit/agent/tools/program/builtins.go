package program

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	"golang.org/x/sync/errgroup"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
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
	OK                  bool                       `json:"ok"`
	Name                tools.ToolName             `json:"name,omitempty"`
	Args                tools.ToolParameters       `json:"args,omitempty"`
	Sequence            int                        `json:"sequence,omitempty"`
	Data                any                        `json:"data"`
	Error               string                     `json:"error"`
	AdmissionReferences []tools.AdmissionReference `json:"-"`
}

type trackedContainer struct {
	Original   []byte
	References []tools.AdmissionReference
}

type runner struct {
	ctx     context.Context
	cancel  context.CancelFunc
	source  *Source
	callID  tools.ToolCallID
	started time.Time
	nextSeq int
	thread  *starlark.Thread

	trackedDicts map[*starlark.Dict]trackedContainer
	trackedLists map[*starlark.List]trackedContainer

	mu               sync.Mutex
	toolCalls        int
	parallelBatches  int
	emitted          bool
	output           any
	outputReferences []tools.AdmissionReference
	scriptErr        *tools.Error
}

func newRunner(ctx context.Context, source *Source, callID tools.ToolCallID, started time.Time) *runner {
	ctx, cancel := context.WithTimeout(ctx, source.limits.Timeout)
	return &runner{
		ctx: ctx, cancel: cancel, source: source, callID: callID, started: started,
		trackedDicts: make(map[*starlark.Dict]trackedContainer),
		trackedLists: make(map[*starlark.List]trackedContainer),
	}
}

func (r *runner) run(script string) (any, []tools.AdmissionReference, Stats, *tools.Error) {
	defer r.cancel()
	if len(script) > r.source.limits.MaxScriptBytes {
		return nil, nil, r.stats(), tools.Budget(op, fmt.Sprintf("script exceeds %d bytes", r.source.limits.MaxScriptBytes))
	}
	thread := &starlark.Thread{Name: "program", Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
		return nil, errors.New("load/import is disabled")
	}}
	thread.SetMaxExecutionSteps(r.source.limits.MaxExecutionSteps)
	r.thread = thread
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
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
	<-exited
	if err != nil {
		if r.ctx.Err() != nil {
			return nil, nil, r.stats(), contextError(r.ctx.Err())
		}
		if errObj := r.takeScriptError(); errObj != nil {
			return nil, nil, r.stats(), errObj
		}
		return nil, nil, r.stats(), tools.Validation(op, err.Error())
	}
	if r.ctx.Err() != nil {
		return nil, nil, r.stats(), contextError(r.ctx.Err())
	}
	r.mu.Lock()
	emitted, output := r.emitted, r.output
	references := append([]tools.AdmissionReference(nil), r.outputReferences...)
	r.mu.Unlock()
	if !emitted {
		return nil, nil, r.stats(), tools.Validation(op, "script did not emit a result")
	}
	return output, references, r.stats(), nil
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
		errObj := tools.Budget(op, fmt.Sprintf("tool call budget exceeded: max %d", r.source.limits.MaxToolCalls))
		r.scriptErr = errObj
		return errObj
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
		r.executeNested(nc, errObj)
		return nil, r.setScriptError(errObj)
	}
	if errObj := r.reserveCalls(1); errObj != nil {
		r.executeNested(nc, errObj)
		return nil, errObj
	}
	res := r.executeNested(nc, nil)
	return r.nestedResultValue(res)
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
		calls = append(calls, nestedCall{Name: tools.ToolName(name), Args: params})
	}
	seqs := r.reserveSequences(len(calls))
	for i := range calls {
		calls[i].Sequence = seqs[i]
	}
	for _, call := range calls {
		if errObj := r.source.findAllowed(r.ctx, call.Name); errObj != nil {
			for _, rejected := range calls {
				r.executeNested(rejected, errObj)
			}
			return nil, r.setScriptError(errObj)
		}
	}
	if errObj := r.reserveCalls(len(calls)); errObj != nil {
		for _, rejected := range calls {
			r.executeNested(rejected, errObj)
		}
		return nil, errObj
	}
	r.mu.Lock()
	r.parallelBatches++
	r.mu.Unlock()
	results := make([]nestedResult, len(calls))
	var g errgroup.Group
	g.SetLimit(r.source.limits.MaxParallelCalls)
	for i, nc := range calls {
		g.Go(func() error {
			results[i] = r.executeNested(nc, nil)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, contextError(err)
	}

	vals := make([]starlark.Value, 0, len(results))
	for _, res := range results {
		v, err := r.nestedResultValue(res)
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
	references := r.emittedReferences(value)
	r.mu.Lock()
	r.emitted = true
	r.output = out
	r.outputReferences = append([]tools.AdmissionReference(nil), references...)
	r.mu.Unlock()
	return starlark.None, nil
}

func (r *runner) executeNested(nc nestedCall, rejected *tools.Error) nestedResult {
	call := tools.ToolCall{
		ID:           tools.ToolCallID(fmt.Sprintf("%s/%d", r.callID, nc.Sequence)),
		ToolName:     nc.Name,
		Arguments:    nc.Args,
		RawArguments: "",
		Status:       tools.ToolCallStatusExecuting,
		CreatedAt:    time.Now(),
	}
	started := time.Now()
	observedCall := call
	observedCall.Arguments = tools.CloneParameters(call.Arguments)
	startedEvent := tools.NestedToolCall{ParentID: r.callID, ChildID: call.ID, Sequence: nc.Sequence, Call: observedCall, Started: started}
	if obs := tools.NestedToolObserverFromContext(r.ctx); obs != nil {
		obs.OnNestedToolStarted(r.ctx, startedEvent)
	}
	var out nestedResult
	var result *tools.ToolResult
	var execErr error
	var kind tools.Kind
	dispatched := false
	defer func() {
		if obs := tools.NestedToolObserverFromContext(r.ctx); obs != nil {
			obs.OnNestedToolFinished(r.ctx, tools.NestedToolResult{NestedToolCall: startedEvent, Dispatched: &dispatched, Result: result, Err: execErr, Kind: kind, Error: out.Error, Duration: time.Since(started)})
		}
	}()
	if rejected != nil {
		execErr = rejected
		kind = rejected.Kind
		out = nestedFailure(nc, rejected.Error())
		return out
	}
	if err := r.ctx.Err(); err != nil {
		errObj := contextError(err)
		execErr = errObj
		kind = errObj.Kind
		out = nestedFailure(nc, errObj.Error())
		return out
	}
	if errObj := r.source.findAllowed(r.ctx, nc.Name); errObj != nil {
		execErr = errObj
		kind = errObj.Kind
		out = nestedFailure(nc, errObj.Error())
		return out
	}
	callCtx := tools.ContextWithWorkspaceWaitCall(r.ctx, tools.WorkspaceWaitCall{ToolID: call.ID, ToolName: call.ToolName, ParentToolID: r.callID, Sequence: nc.Sequence})
	dispatched = true
	res, err := r.source.inner.Execute(callCtx, call)
	result, execErr = res, err
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		errObj := contextError(ctxErr)
		r.thread.Cancel(ctxErr.Error())
		execErr = errObj
		kind = errObj.Kind
		out = nestedFailure(nc, errObj.Error())
		return out
	}
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
	data, err := normalizeJSON(res.Data, r.source.limits.MaxToolResultBytes)
	if err != nil {
		execErr = err
		out = nestedFailure(nc, err.Error())
		return out
	}
	if res.Success {
		out = nestedSuccess(nc, data)
	} else {
		message := res.Error
		if message == "" {
			message = "tool call failed"
		}
		out = nestedFailure(nc, message)
		// Failed tasks can contain useful partial results. Track their complete
		// envelope just like success; discarded/transformed output is not delivery.
		out.Data = data
	}
	out.AdmissionReferences = append([]tools.AdmissionReference(nil), res.AdmissionReferences...)
	return out
}

func nestedSuccess(call nestedCall, data any) nestedResult {
	return nestedResult{OK: true, Name: call.Name, Args: call.Args, Sequence: call.Sequence, Data: data}
}

func nestedFailure(call nestedCall, msg string) nestedResult {
	return nestedResult{OK: false, Name: call.Name, Args: call.Args, Sequence: call.Sequence, Error: msg}
}

func (r *runner) nestedResultValue(res nestedResult) (starlark.Value, error) {
	m := map[string]any{"ok": res.OK, "data": res.Data, "error": res.Error}
	if res.Name != "" {
		m["name"] = res.Name.String()
	}
	if len(res.Args) > 0 {
		m["args"] = map[string]any(res.Args)
	}
	envelope, err := toStarlarkDict(m)
	if err != nil {
		return nil, err
	}
	if len(res.AdmissionReferences) == 0 {
		return envelope, nil
	}
	r.trackContainer(envelope, res.AdmissionReferences)
	if !res.OK {
		// Partial data without its failure identity is not the full child result.
		return envelope, nil
	}
	data, found, err := envelope.Get(starlark.String("data"))
	if err != nil {
		return nil, err
	}
	if found {
		r.trackContainer(data, res.AdmissionReferences)
	}
	return envelope, nil
}

func (r *runner) trackContainer(value starlark.Value, references []tools.AdmissionReference) {
	original, err := starlarkJSON(value)
	if err != nil {
		return
	}
	tracked := trackedContainer{
		Original:   append([]byte(nil), original...),
		References: append([]tools.AdmissionReference(nil), references...),
	}
	switch value := value.(type) {
	case *starlark.Dict:
		r.trackedDicts[value] = tracked
	case *starlark.List:
		r.trackedLists[value] = tracked
	}
}

func (r *runner) emittedReferences(value starlark.Value) []tools.AdmissionReference {
	seenDicts := make(map[*starlark.Dict]struct{})
	seenLists := make(map[*starlark.List]struct{})
	references := make(map[tools.AdmissionReference]struct{})
	var visit func(starlark.Value)
	visit = func(value starlark.Value) {
		switch value := value.(type) {
		case *starlark.Dict:
			if _, seen := seenDicts[value]; seen {
				return
			}
			seenDicts[value] = struct{}{}
			if tracked, ok := r.trackedDicts[value]; ok && containerUnchanged(value, tracked.Original) {
				for _, reference := range tracked.References {
					references[reference] = struct{}{}
				}
			}
			for _, item := range value.Items() {
				visit(item[0])
				visit(item[1])
			}
		case *starlark.List:
			if _, seen := seenLists[value]; seen {
				return
			}
			seenLists[value] = struct{}{}
			if tracked, ok := r.trackedLists[value]; ok && containerUnchanged(value, tracked.Original) {
				for _, reference := range tracked.References {
					references[reference] = struct{}{}
				}
			}
			for i := range value.Len() {
				visit(value.Index(i))
			}
		case starlark.Tuple:
			for i := range value.Len() {
				visit(value.Index(i))
			}
		}
	}
	visit(value)
	out := make([]tools.AdmissionReference, 0, len(references))
	for reference := range references {
		out = append(out, reference)
	}
	return out
}

func containerUnchanged(value starlark.Value, original []byte) bool {
	current, err := starlarkJSON(value)
	return err == nil && bytes.Equal(current, original)
}

func starlarkJSON(value starlark.Value) ([]byte, error) {
	raw, err := fromStarlark(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}
