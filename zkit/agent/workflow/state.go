package workflow

import "maps"

// State records workflow execution data.
type State struct {
	Input   any
	Current NodeID
	Values  map[NodeID]any
	Path    []NodeID
}

func newState(input any) State { return State{Input: input, Values: map[NodeID]any{Start: input}} }

func (s State) clone() State {
	out := s
	out.Path = append([]NodeID(nil), s.Path...)
	out.Values = map[NodeID]any{}
	maps.Copy(out.Values, s.Values)
	return out
}
