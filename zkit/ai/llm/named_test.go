package llm_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// recordingProvider is a minimal Provider that records the arguments
// Complete was called with, so we can prove Named delegates verbatim.
type recordingProvider struct {
	name    string
	gotReq  llm.CompletionRequest
	gotCtx  context.Context
	retSeq  iter.Seq2[llm.CompletionChunk, error]
	retErr  error
	callCnt int
}

func (p *recordingProvider) Name() string { return p.name }

func (p *recordingProvider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	p.callCnt++
	p.gotCtx = ctx
	p.gotReq = req
	return p.retSeq, p.retErr
}

func TestNamedOverridesNameButDelegatesComplete(t *testing.T) {
	marker := llm.CompletionChunk{Content: "delegated"}
	seq := iter.Seq2[llm.CompletionChunk, error](func(yield func(llm.CompletionChunk, error) bool) {
		yield(marker, nil)
	})
	sentinel := errors.New("boom")
	inner := &recordingProvider{name: "openai", retSeq: seq, retErr: sentinel}

	wrapped := llm.Named(inner, "llamacpp")

	if got := wrapped.Name(); got != "llamacpp" {
		t.Fatalf("Name() = %q, want %q", got, "llamacpp")
	}
	if inner.Name() != "openai" {
		t.Fatalf("inner Name() mutated to %q", inner.Name())
	}

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("k"), "v")
	req := llm.CompletionRequest{Temperature: 0.42, MaxTokens: 99}

	gotSeq, gotErr := wrapped.Complete(ctx, req)

	if inner.callCnt != 1 {
		t.Fatalf("inner.Complete called %d times, want 1", inner.callCnt)
	}
	if !errors.Is(gotErr, sentinel) {
		t.Fatalf("err = %v, want sentinel", gotErr)
	}
	// Function values are not comparable; prove the returned sequence is
	// the inner one by draining it and checking the marker chunk.
	if gotSeq == nil {
		t.Fatal("returned sequence is nil")
	}
	var got []llm.CompletionChunk
	for c := range gotSeq {
		got = append(got, c)
	}
	if len(got) != 1 || got[0].Content != marker.Content {
		t.Fatalf("returned sequence is not the inner sequence: %+v", got)
	}
	if inner.gotCtx.Value(ctxKey("k")) != "v" {
		t.Fatal("ctx not threaded to inner.Complete")
	}
	if inner.gotReq.Temperature != 0.42 || inner.gotReq.MaxTokens != 99 {
		t.Fatalf("req not threaded verbatim: %+v", inner.gotReq)
	}
}
