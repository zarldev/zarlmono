package providertest_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestRunPreCanceledStreamDoesNoRequestIO(t *testing.T) {
	var requests atomic.Int64
	providertest.Run(t, providertest.Suite{
		Factory: func(*testing.T, string) llm.Provider {
			return fixtureProvider{complete: func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					yield(llm.CompletionChunk{}, context.Cause(ctx))
				}
			}}
		},
		Scenarios: []providertest.Scenario{{
			Name: "pre-canceled",
			Handler: func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			},
			Request:   providertest.SimpleRequest("unused"),
			PreCancel: true,
			Assert: func(t *testing.T, chunks []llm.CompletionChunk, streamErr error) {
				providertest.AssertPreCanceled(t, chunks, streamErr)
				if got := requests.Load(); got != 0 {
					t.Errorf("requests = %d, want 0", got)
				}
			},
		}},
	})
}

func TestRunPropagatesDownstreamFalseSynchronously(t *testing.T) {
	providertest.Run(t, providertest.Suite{
		Factory: func(*testing.T, string) llm.Provider {
			return fixtureProvider{complete: func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					if !yield(llm.CompletionChunk{Content: "accepted"}, nil) {
						return
					}
					yield(llm.CompletionChunk{Content: "must not be observed"}, nil)
				}
			}}
		},
		Scenarios: []providertest.Scenario{{
			Name:      "downstream-false",
			Handler:   func(http.ResponseWriter, *http.Request) {},
			Request:   providertest.SimpleRequest("unused"),
			StopAfter: 1,
			Assert: func(t *testing.T, chunks []llm.CompletionChunk, streamErr error) {
				providertest.AssertStoppedByConsumer(t, chunks, streamErr)
				if got := providertest.CollectContent(chunks); got != "accepted" {
					t.Errorf("content = %q, want accepted", got)
				}
			},
		}},
	})
}

type fixtureProvider struct {
	complete func(context.Context, llm.CompletionRequest) llm.CompletionStream
}

func (p fixtureProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return p.complete(ctx, req)
}

func (fixtureProvider) Name() string { return "fixture" }
