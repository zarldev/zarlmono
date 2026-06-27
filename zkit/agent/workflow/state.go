package workflow

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
	for k, v := range s.Values {
		out.Values[k] = v
	}
	return out
}
