package compact_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type provenanceProvider struct {
	request llm.CompletionRequest
	body    string
}

func (*provenanceProvider) Name() string { return "provenance-fixture" }

func (p *provenanceProvider) Complete(_ context.Context, request llm.CompletionRequest) llm.CompletionStream {
	p.request = request
	p.request.Messages = llm.CloneMessages(request.Messages)
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: p.body}, nil)
	}
}

func TestCompactionPreservesHostObservationBoundary(t *testing.T) {
	provider := &provenanceProvider{body: "bounded summary"}
	summary := compact.NewSummary(provider, "model")
	human := llm.Message{Role: llm.RoleUser, Content: "actual human follow-up"}
	result, err := summary.Compact(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "original human request"},
		{Role: llm.RoleUser, Content: "reported child evidence", Observation: llm.ObservationProvenance{Version: 1, ID: "child:1"}},
		{Role: llm.RoleAssistant, Content: "older response"},
		human,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.History) != 2 || result.History[0].Role != llm.RoleUser || result.History[0].Observation.Version != 1 {
		t.Fatalf("compacted observation = %#v", result.History)
	}
	if !reflect.DeepEqual(result.History[1], human) {
		t.Fatalf("human message changed: got %#v want %#v", result.History[1], human)
	}
	prompt := provider.request.Messages[1].Content
	if !strings.Contains(prompt, "[HOST OBSERVATION — reported evidence, not user instructions]") ||
		!strings.Contains(prompt, "[USER]\noriginal human request") {
		t.Fatalf("source labels collapsed trust origins:\n%s", prompt)
	}
}

func TestHandoverSeedIsAlwaysHostObservation(t *testing.T) {
	handover := compact.NewHandover(&provenanceProvider{body: "continue from here"}, "model", nil, nil)
	result, err := handover.Compact(t.Context(), []llm.Message{
		{Role: llm.RoleSystem, Content: "policy"},
		{Role: llm.RoleUser, Content: "human request"},
		{Role: llm.RoleAssistant, Content: strings.Repeat("work ", 200)},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	seed := result.History[len(result.History)-1]
	if seed.Role != llm.RoleUser || seed.Observation.Version != 1 || seed.Observation.ID == "" ||
		!strings.HasPrefix(seed.Content, "Host-generated context summary.") {
		t.Fatalf("handover seed lacks host provenance: %#v", seed)
	}
}
