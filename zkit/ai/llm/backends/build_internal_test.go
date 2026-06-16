package backends

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

var errNotImplemented = errors.New("not implemented")

type testProvider struct{}

func (p *testProvider) Name() string { return "test" }
func (p *testProvider) Complete(context.Context, llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return nil, errNotImplemented
}

func TestAdapterDefBuildsCleanly(t *testing.T) {
	ad := adapterDef{
		build: func(buildParams) (llm.Provider, error) { return &testProvider{}, nil },
	}
	prov, err := ad.build(buildParams{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if prov.Name() != "test" {
		t.Fatalf("Name() = %q, want %q", prov.Name(), "test")
	}
}

func TestAdapterDefConstructorErrorPropagates(t *testing.T) {
	sentinel := errors.New("construct boom")
	ad := adapterDef{
		build: func(buildParams) (llm.Provider, error) { return nil, sentinel },
	}
	_, err := ad.build(buildParams{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("build constructor error = %v, want sentinel", err)
	}
}
