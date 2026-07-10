package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
)

// NodeID identifies a workflow node.
type NodeID string

// String returns the node identifier as a string.
func (id NodeID) String() string { return string(id) }

// Start and End are sentinel node names for graph boundaries.
const (
	Start NodeID = "__start__"
	End   NodeID = "__end__"
)

// Graph is a mutable workflow builder. Compile freezes it into a Runnable.
type Graph struct {
	nodes  map[NodeID]anyNode
	edges  map[NodeID]NodeID
	routes map[NodeID]Route
}

// NewGraph returns an empty workflow graph.
func NewGraph() *Graph {
	return &Graph{nodes: map[NodeID]anyNode{}, edges: map[NodeID]NodeID{}, routes: map[NodeID]Route{}}
}

// AddNode registers a node by name.
func AddNode[I, O any](g *Graph, name string, node Node[I, O]) error {
	id := NodeID(name)
	if name == "" || id == Start || id == End {
		return fmt.Errorf("add node: invalid name %q", name)
	}
	if g.nodes == nil {
		g.nodes = map[NodeID]anyNode{}
	}
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("add node %q: already exists", name)
	}
	wrapped := WrapNode(node)
	g.nodes[id] = func(ctx context.Context, input any) (any, error) {
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
	fromID, toID := NodeID(from), NodeID(to)
	if fromID == "" || toID == "" {
		return errors.New("add edge: names are required")
	}
	if g.edges == nil {
		g.edges = map[NodeID]NodeID{}
	}
	g.edges[fromID] = toID
	return nil
}

// AddConditionalEdge adds a dynamic route after from runs.
func (g *Graph) AddConditionalEdge(from string, route Route) error {
	fromID := NodeID(from)
	if fromID == "" || fromID == End {
		return fmt.Errorf("add conditional edge: invalid from %q", from)
	}
	if route == nil {
		return fmt.Errorf("add conditional edge %q: route is nil", from)
	}
	if g.routes == nil {
		g.routes = map[NodeID]Route{}
	}
	g.routes[fromID] = route
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
	nodes := map[NodeID]anyNode{}
	maps.Copy(nodes, g.nodes)
	edges := map[NodeID]NodeID{}
	maps.Copy(edges, g.edges)
	routes := map[NodeID]Route{}
	maps.Copy(routes, g.routes)
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
