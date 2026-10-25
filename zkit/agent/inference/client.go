package inference

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type client interface {
	Complete(context.Context, llm.CompletionRequest) llm.CompletionStream
}

// Complete defers both client construction and iteration until account admission.
// The concrete provider remains unchanged, including its protocol qualification.
func (s Scope) Complete(ctx context.Context, call Call, source client, req llm.CompletionRequest) llm.CompletionStream {
	return s.Wrap(ctx, call, func(yield func(llm.CompletionChunk, error) bool) { source.Complete(ctx, req)(yield) })
}

// Invoke admits one call, constructs its stream, then synchronously hands it to
// consume. consume must drain or stop the stream before returning. Only admission
// errors are returned; provider errors retain their ordinary stream semantics.
// Separating construction from consumption preserves runner attempt timing.
func (s Scope) Invoke(ctx context.Context, call Call, source client, req llm.CompletionRequest, consume func(llm.CompletionStream)) error {
	return s.invoke(ctx, call, func(observe func(llm.CompletionStream) llm.CompletionStream) {
		consume(observe(source.Complete(ctx, req)))
	})
}
