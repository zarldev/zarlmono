package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Runnable is a compiled workflow graph.
type Runnable struct {
	nodes    map[NodeID]anyNode
	edges    map[NodeID]NodeID
	routes   map[NodeID]Route
	MaxSteps int
	Sink     EventSink
}

// Invoke runs the workflow and returns the final value.
func (r *Runnable) Invoke(ctx context.Context, input any) (any, error) {
	out, _, err := r.InvokeState(ctx, input)
	return out, err
}

// InvokeState runs the workflow and returns the final value and execution state.
func (r *Runnable) InvokeState(ctx context.Context, input any) (any, State, error) {
	if r == nil {
		return nil, State{}, errors.New("invoke workflow: runnable is nil")
	}
	started := time.Now()
	if r.Sink != nil {
		r.Sink.OnWorkflowStarted(Started{Input: input})
	}
	current := r.edges[Start]
	state := newState(input)
	value := input
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 128
	}
	for range maxSteps {
		if err := ctx.Err(); err != nil {
			return r.fail(err, state, started)
		}
		if current == End {
			final := state.clone()
			if r.Sink != nil {
				r.Sink.OnWorkflowCompleted(Completed{Output: value, State: final, Duration: time.Since(started)})
			}
			return value, final, nil
		}
		node, ok := r.nodes[current]
		if !ok {
			return r.fail(fmt.Errorf("invoke workflow: node %q not found", current), state, started)
		}
		state.Current = current
		state.Path = append(state.Path, current)
		if r.Sink != nil {
			r.Sink.OnWorkflowNodeStarted(NodeStarted{Node: current, Input: value})
		}
		nodeStarted := time.Now()
		out, err := node(ctx, value)
		if err != nil {
			wrapped := fmt.Errorf("run node %q: %w", current, err)
			if r.Sink != nil {
				r.Sink.OnWorkflowNodeFailed(NodeFailed{Node: current, Error: wrapped, Duration: time.Since(nodeStarted)})
			}
			return r.fail(wrapped, state, started)
		}
		if r.Sink != nil {
			r.Sink.OnWorkflowNodeCompleted(NodeCompleted{Node: current, Output: out, Duration: time.Since(nodeStarted)})
		}
		value = out
		state.Values[current] = out
		if route := r.routes[current]; route != nil {
			next, err := route(ctx, state.clone())
			if err != nil {
				return r.fail(fmt.Errorf("route node %q: %w", current, err), state, started)
			}
			current = next
		} else {
			current = r.edges[current]
		}
		if current == "" {
			return r.fail(fmt.Errorf("invoke workflow: node %q has no outgoing edge", state.Current), state, started)
		}
	}
	return r.fail(fmt.Errorf("invoke workflow: exceeded max steps %d", maxSteps), state, started)
}

func (r *Runnable) fail(err error, state State, started time.Time) (any, State, error) {
	snapshot := state.clone()
	if r.Sink != nil {
		r.Sink.OnWorkflowFailed(Failed{Error: err, State: snapshot, Duration: time.Since(started)})
	}
	return nil, snapshot, err
}
