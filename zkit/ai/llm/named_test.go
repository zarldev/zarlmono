package llm_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type recordingProvider struct {
	name       string
	gotReq     llm.CompletionRequest
	gotCtx     context.Context
	retStream  llm.CompletionStream
	callCnt    int
	invokedCnt int
}

func (p *recordingProvider) Name() string { return p.name }

func (p *recordingProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	p.callCnt++
	p.gotCtx = ctx
	p.gotReq = req
	return p.retStream
}

func TestNamedOverridesNameAndDelegatesLazyStream(t *testing.T) {
	marker := llm.CompletionChunk{Content: "delegated"}
	terminalErr := errors.New("boom")
	inner := &recordingProvider{name: "openai"}
	inner.retStream = func(yield func(llm.CompletionChunk, error) bool) {
		inner.invokedCnt++
		if !yield(marker, nil) {
			return
		}
		yield(llm.CompletionChunk{}, terminalErr)
	}

	wrapped := llm.Named(inner, "llamacpp")
	if got := wrapped.Name(); got != "llamacpp" {
		t.Fatalf("Name() = %q, want %q", got, "llamacpp")
	}
	if inner.Name() != "openai" {
		t.Fatalf("inner Name() mutated to %q", inner.Name())
	}

	type ctxKey string
	ctx := context.WithValue(t.Context(), ctxKey("k"), "v")
	req := llm.CompletionRequest{Temperature: 0.42, MaxTokens: 99}
	stream := wrapped.Complete(ctx, req)

	if inner.callCnt != 1 {
		t.Fatalf("inner.Complete called %d times, want 1", inner.callCnt)
	}
	if inner.invokedCnt != 0 {
		t.Fatalf("inner stream invoked %d times before range, want 0", inner.invokedCnt)
	}
	if stream == nil {
		t.Fatal("returned stream is nil")
	}
	if inner.gotCtx.Value(ctxKey("k")) != "v" {
		t.Fatal("ctx not threaded to inner.Complete")
	}
	if inner.gotReq.Temperature != 0.42 || inner.gotReq.MaxTokens != 99 {
		t.Fatalf("req not threaded verbatim: %+v", inner.gotReq)
	}

	var chunks []llm.CompletionChunk
	var errs []error
	for chunk, err := range stream {
		chunks = append(chunks, chunk)
		errs = append(errs, err)
	}
	if inner.invokedCnt != 1 {
		t.Fatalf("inner stream invoked %d times, want 1", inner.invokedCnt)
	}
	if len(chunks) != 2 || chunks[0].Content != marker.Content || !reflect.ValueOf(chunks[1]).IsZero() {
		t.Fatalf("delegated chunks = %#v, want marker then zero terminal chunk", chunks)
	}
	if errs[0] != nil || !errors.Is(errs[1], terminalErr) {
		t.Fatalf("delegated errors = %v, want nil then terminal error", errs)
	}
}
