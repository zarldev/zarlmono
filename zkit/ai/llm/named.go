package llm

import "context"

// Named returns a Provider that delegates to inner but reports name from
// Name(). Useful when one adapter type (e.g. openai.Provider) is reused
// under different provider identities — llamacpp and ollama both wrap
// openai.Provider, and the registry needs their Name() to reflect the
// wrapper identity rather than "openai".
func Named(inner Provider, name string) Provider {
	return &named{inner: inner, name: name}
}

type named struct {
	inner Provider
	name  string
}

func (n *named) Complete(ctx context.Context, req CompletionRequest) CompletionStream {
	return n.inner.Complete(ctx, req)
}

func (n *named) Name() string { return n.name }

// Unwrap returns the adapter whose behavior Named preserves. Consumers can
// inspect adapter capabilities without mistaking a configured route name for
// an implementation guarantee.
func (n *named) Unwrap() Provider { return n.inner }
