package workflow

import "context"

// Node transforms an input value into an output value.
type Node[I, O any] interface {
	Run(ctx context.Context, input I) (O, error)
}

// NodeFunc adapts a function to Node.
type NodeFunc[I, O any] func(context.Context, I) (O, error)

// Run calls f itself.
func (f NodeFunc[I, O]) Run(ctx context.Context, input I) (O, error) { return f(ctx, input) }

type anyNode func(context.Context, any) (any, error)

// WrapNode adapts a typed node for use in Graph.
func WrapNode[I, O any](n Node[I, O]) anyNode {
	return func(ctx context.Context, input any) (any, error) {
		v, ok := input.(I)
		if !ok {
			var zero O
			return zero, TypeMismatchError{Node: "", Want: typeName[I](), Got: input}
		}
		return n.Run(ctx, v)
	}
}
