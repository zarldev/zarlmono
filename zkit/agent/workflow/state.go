package workflow

import "maps"

// State records workflow execution data.
type State struct {
	Input   any
	Current string
	Values  map[string]any
	Path    []string
}

func newState(input any) State { return State{Input: input, Values: map[string]any{Start: input}} }

func (s State) clone() State {
	out := s
	out.Path = append([]string(nil), s.Path...)
	out.Values = map[string]any{}
	maps.Copy(out.Values, s.Values)
	return out
}
