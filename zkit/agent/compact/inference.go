package compact

import (
	"context"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Inference attributes one compactor invocation to its configured target.
// Model is the composition root's label for the provider-bound model; an empty
// label is unknown, never the task model. Usage nil means unreported, not zero.
// CompletionTokens already includes any reported reasoning subtotal.
type Inference struct {
	Provider string
	Model    string
	Usage    *llm.Usage
	Duration time.Duration
}

func collectInference(ctx context.Context, provider llm.Provider, model string, req llm.CompletionRequest, engine string, scope inference.Scope) (string, *Inference, error) {
	started := time.Now()
	stream := scope.Complete(ctx, inference.Call{Operation: "compaction", Provider: provider.Name(), Model: model}, provider, req)
	body, usage, err := collectContent(stream, engine)
	return body, &Inference{Provider: provider.Name(), Model: model, Usage: usage, Duration: time.Since(started)}, err
}
