package runner

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Client is the runner's view of an LLM. Smaller than llm.Provider —
// the runner only needs streaming completion, not model discovery,
// image generation, or capability introspection. Implementations are
// expected to surface mid-stream errors via the second value of the
// iter.Seq2 yield, not via a field on the Chunk.
type Client interface {
	Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error)
}

// ClientFromProvider narrows an llm.Provider to the runner's Client view.
// Now that Provider.Complete returns an iter.Seq2, a Provider satisfies
// Client directly — this is just the explicit narrowing seam (the runner
// depends on Client, not the wider Provider).
func ClientFromProvider(p llm.Provider) Client {
	return p
}
