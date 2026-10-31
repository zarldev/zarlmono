package compact_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type accountingProvider struct {
	chunks []llm.CompletionChunk
	err    error
}

func (accountingProvider) Name() string { return "compactor-provider" }
func (p accountingProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		for _, chunk := range p.chunks {
			if !yield(chunk, nil) {
				return
			}
		}
		if p.err != nil {
			yield(llm.CompletionChunk{}, p.err)
		}
	}
}

func TestCompactorInferenceAccounting(t *testing.T) {
	failure := errors.New("stream interrupted")
	for _, tc := range []struct {
		name     string
		chunks   []llm.CompletionChunk
		err      error
		reported bool
		tokens   int
	}{
		{"success", []llm.CompletionChunk{{UsageReported: true, Usage: llm.Usage{PromptTokens: 2}}, {Content: "summary", UsageReported: true, Usage: llm.Usage{PromptTokens: 5}}}, nil, true, 5},
		{"failed after usage", []llm.CompletionChunk{{UsageReported: true, Usage: llm.Usage{PromptTokens: 5}}}, failure, true, 5},
		{"reported zero", []llm.CompletionChunk{{Content: "summary", UsageReported: true}}, nil, true, 0},
		{"missing usage", []llm.CompletionChunk{{Content: "summary"}}, nil, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := accountingProvider{chunks: tc.chunks, err: tc.err}
			account := inference.New()
			scope := inference.Scope{Account: account}
			for _, engine := range []compact.Compactor{compact.NewSummary(provider, "summary-model"), compact.NewExecutive(provider, "summary-model", &fakeStateProvider{}), compact.NewHandover(provider, "summary-model", &fakeStateProvider{}, nil)} {
				switch c := engine.(type) {
				case *compact.Summary:
					c.InferenceScope = scope
				case *compact.Executive:
					c.InferenceScope = scope
				case *compact.Handover:
					c.InferenceScope = scope
				}
				result, err := engine.Compact(t.Context(), []llm.Message{{Role: llm.RoleUser, Content: "old"}, {Role: llm.RoleAssistant, Content: "answer"}, {Role: llm.RoleUser, Content: "recent"}}, 1)
				if !errors.Is(err, tc.err) {
					t.Fatalf("%T: error = %v", engine, err)
				}
				inference := result.Inference
				if inference == nil || inference.Provider != "compactor-provider" || inference.Model != "summary-model" || inference.Duration < 0 {
					t.Fatalf("%T: inference = %+v", engine, inference)
				}
				if (inference.Usage != nil) != tc.reported {
					t.Fatalf("usage presence = %v", inference.Usage)
				}
				if inference.Usage != nil && inference.Usage.PromptTokens != tc.tokens {
					t.Errorf("usage = %+v", inference.Usage)
				}
			}
		})
	}
}
