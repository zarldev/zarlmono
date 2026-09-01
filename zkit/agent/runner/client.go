package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Client is the runner's view of an LLM. Smaller than llm.Provider —
// the runner only needs streaming completion, not model discovery,
// image generation, or capability introspection. Implementations are
// expected to surface terminal failures through the stream's error value.
type Client interface {
	Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream
}

// ClientFromProvider narrows an llm.Provider to the runner's Client view.
// llm.Provider satisfies Client directly; this function keeps the explicit
// narrowing seam because the runner depends on Client, not Provider.
func ClientFromProvider(p llm.Provider) Client {
	return p
}
