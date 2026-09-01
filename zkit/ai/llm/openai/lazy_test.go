package openai_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestCompleteIsLazyAndPreCanceledYieldsCauseOnce(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer srv.Close()

	provider, err := openai.NewProvider("test-key", openai.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	cause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	stream := provider.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Stream: true})
	if requests.Load() != 0 {
		t.Fatalf("requests before iteration = %d, want 0", requests.Load())
	}

	var chunks []llm.CompletionChunk
	var errs []error
	for chunk, streamErr := range stream {
		chunks = append(chunks, chunk)
		errs = append(errs, streamErr)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests after pre-canceled iteration = %d, want 0", requests.Load())
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %#v, want one zero chunk", chunks)
	}
	zero := chunks[0]
	if zero.Content != "" || zero.Thinking != "" || len(zero.ToolCalls) != 0 || zero.FinishReason != llm.FinishReasons.UNKNOWN || zero.UsageReported || zero.Usage != (llm.Usage{}) {
		t.Fatalf("chunk = %#v, want zero chunk", zero)
	}
	if len(errs) != 1 || !errors.Is(errs[0], cause) {
		t.Fatalf("errors = %#v, want cause", errs)
	}
}

func TestCompleteStopsImmediatelyWhenYieldReturnsFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"one\"},\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"two\"},\"finish_reason\":\"stop\",\"index\":0}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	provider, err := openai.NewProvider("test-key", openai.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	calls := 0
	provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Stream: true})(func(llm.CompletionChunk, error) bool {
		calls++
		return false
	})
	if calls != 1 {
		t.Fatalf("yield calls = %d, want 1", calls)
	}
}
