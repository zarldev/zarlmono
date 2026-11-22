package compact

import (
	"context"
	"fmt"
	"strings"
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
	// PricingUsage and Execution own the latest independently supplied sidecars.
	// Nil means absent; present unknown and known zero remain distinct. Each
	// non-nil snapshot replaces its predecessor completely; later nil retains it.
	PricingUsage *llm.PricingUsage
	Execution    *llm.ExecutionObservation
	Duration     time.Duration
}

// Clone returns a deep-owned snapshot, preserving absence and reported zero.
// A plain value copy borrows Usage and both sidecars, including nested storage.
func (o Inference) Clone() Inference {
	o.Provider = strings.Clone(o.Provider)
	o.Model = strings.Clone(o.Model)
	if o.Usage != nil {
		o.Usage = new(*o.Usage)
	}
	if o.PricingUsage != nil {
		usage := o.PricingUsage.Clone()
		o.PricingUsage = &usage
	}
	if o.Execution != nil {
		execution := o.Execution.Clone()
		o.Execution = &execution
	}
	return o
}

func collectInference(ctx context.Context, provider llm.Provider, model string, req llm.CompletionRequest, engine string, scope inference.Scope) (string, *Inference, error) {
	started := time.Now()
	providerName := provider.Name()
	var body string
	var observation *Inference
	var streamErr error
	err := scope.Invoke(ctx, inference.Call{Operation: "compaction", Provider: providerName, Model: model}, provider, req, func(stream llm.CompletionStream) {
		observation = &Inference{Provider: providerName, Model: model}
		body, streamErr = collectContent(stream, engine, observation)
		observation.Duration = time.Since(started)
	})
	if err != nil {
		return "", nil, fmt.Errorf("compact.%s: stream: %w", engine, err)
	}
	return body, observation, streamErr
}
