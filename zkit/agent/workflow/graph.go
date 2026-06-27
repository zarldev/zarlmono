package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// Start and End are sentinel node names for graph boundaries.
const (
	Start = "__start__"
	End   = "__end__"
)

// Graph is a mutable workflow builder. Compile freezes it into a Runnable.
type Graph struct {
	nodes  map[string]anyNode
	edges  map[string]string
	routes map[string]Route
}

// NewGraph returns an empty workflow graph.
func NewGraph() *Graph {
	return &Graph{nodes: map[string]anyNode{}, edges: map[string]string{}, routes: map[string]Route{}}
}

// AddNode registers a node by name.
func AddNode[I, O any](g *Graph, name string, node Node[I, O]) error {
	if name == "" || name == Start || name == End {
		return fmt.Errorf("add node: invalid name %q", name)
	}
	if g.nodes == nil {
		g.nodes = map[string]anyNode{}
	}
	if _, exists := g.nodes[name]; exists {
		return fmt.Errorf("add node %q: already exists", name)
	}
	wrapped := WrapNode(node)
	g.nodes[name] = func(ctx context.Context, input any) (any, error) {
		out, err := wrapped(ctx, input)
		if mismatch, ok := errors.AsType[TypeMismatchError](err); ok {
			mismatch.Node = name
			return nil, mismatch
		}
		return out, err
	}
	return nil
}

// AddEdge adds a static edge from one node to another. from may be Start and to may be End.
func (g *Graph) AddEdge(from, to string) error {
	if from == "" || to == "" {
		return errors.New("add edge: names are required")
	}
	if g.edges == nil {
		g.edges = map[string]string{}
	}
	g.edges[from] = to
	return nil
}

// AddConditionalEdge adds a dynamic route after from runs.
func (g *Graph) AddConditionalEdge(from string, route Route) error {
	if from == "" || from == End {
		return fmt.Errorf("add conditional edge: invalid from %q", from)
	}
	if route == nil {
		return fmt.Errorf("add conditional edge %q: route is nil", from)
	}
	if g.routes == nil {
		g.routes = map[string]Route{}
	}
	g.routes[from] = route
	return nil
}

// Compile validates g and returns an executable workflow.
func (g *Graph) Compile() (*Runnable, error) {
	if g == nil {
		return nil, errors.New("compile workflow: graph is nil")
	}
	start, ok := g.edges[Start]
	if !ok || start == End {
		return nil, errors.New("compile workflow: start edge is required")
	}
	if _, ok := g.nodes[start]; !ok {
		return nil, fmt.Errorf("compile workflow: start target %q is not a node", start)
	}
	nodes := map[string]anyNode{}
	for k, v := range g.nodes {
		nodes[k] = v
	}
	edges := map[string]string{}
	for k, v := range g.edges {
		edges[k] = v
	}
	routes := map[string]Route{}
	for k, v := range g.routes {
		routes[k] = v
	}
	return &Runnable{nodes: nodes, edges: edges, routes: routes}, nil
}

// TypeMismatchError reports a node input type mismatch.
type TypeMismatchError struct {
	Node, Want string
	Got        any
}

// Error formats the mismatch.
func (e TypeMismatchError) Error() string {
	return fmt.Sprintf("workflow node %q: input type mismatch, want %s got %T", e.Node, e.Want, e.Got)
}

func typeName[T any]() string {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		return "<nil>"
	}
	return t.String()
}
