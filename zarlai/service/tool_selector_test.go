package service_test

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// simpleToolDefinition is a minimal tools.Tool used to exercise the selector
// without real tool implementations.
type simpleToolDefinition struct{ name, desc string }

func (s simpleToolDefinition) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: tools.ToolName(s.name), Description: s.desc}
}

func (s simpleToolDefinition) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, ""), nil
}

// fakeEmbedder returns a fixed vector per text by hashing the first
// character into a sparse one-hot vector. Deterministic and trivially
// scored — enough to verify the plumbing without a real embedder.
// mu guards calls so the fake is safe for concurrent use.
type fakeEmbedder struct {
	mu    sync.Mutex
	dim   int
	calls int
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	v := make([]float32, f.dim)
	if len(text) == 0 {
		return v, nil
	}
	v[int(text[0])%f.dim] = 1
	return v, nil
}

func TestToolSelectorBuildsIndexOnce(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(simpleToolDefinition{name: "a", desc: "alpha"})
	r.Register(simpleToolDefinition{name: "b", desc: "bravo"})
	r.Register(simpleToolDefinition{name: "c", desc: "charlie"})

	emb := &fakeEmbedder{dim: 32}
	sel := service.NewToolSelector(r, emb)

	if err := sel.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if emb.calls != 3 {
		t.Fatalf("first build: expected 3 embed calls, got %d", emb.calls)
	}

	// Second call without registry changes must not re-embed.
	if err := sel.EnsureIndex(context.Background()); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if emb.calls != 3 {
		t.Fatalf("second build: expected still 3 calls, got %d", emb.calls)
	}
}

func TestToolSelectorRebuildsOnRegistryChange(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(simpleToolDefinition{name: "a", desc: "alpha"})

	emb := &fakeEmbedder{dim: 32}
	sel := service.NewToolSelector(r, emb)

	if err := sel.EnsureIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if emb.calls != 1 {
		t.Fatalf("initial: got %d calls", emb.calls)
	}

	r.Register(simpleToolDefinition{name: "b", desc: "bravo"})
	if err := sel.EnsureIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if emb.calls != 3 { // rebuilds fully: a + b re-embedded on version change
		t.Fatalf("after change: got %d calls, want 3", emb.calls)
	}
}

func TestToolSelectorConcurrentEnsureIndex(t *testing.T) {
	r := tools.NewRegistry()
	for i := range 10 {
		r.Register(simpleToolDefinition{name: fmt.Sprintf("t%02d", i), desc: "x"})
	}
	emb := &fakeEmbedder{dim: 16}
	sel := service.NewToolSelector(r, emb)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := sel.EnsureIndex(context.Background()); err != nil {
				t.Errorf("ensure: %v", err)
			}
		})
	}
	wg.Wait()

	// With concurrent builders, embed calls may exceed 10 (workers racing
	// before any has stamped a version) but must stay bounded — 80 (8
	// workers × 10 tools) is the worst case, and we don't care about the
	// exact number. The point is no deadlock and no error.
	if emb.calls == 0 {
		t.Fatalf("expected at least one build, got 0 embed calls")
	}
}

// TestToolSelector_KeywordMatchOverridesSemanticRank locks in the
// fix for the class of bug where the user literally echoes a tool's
// trigger words but the embedding ranks the tool just outside topN.
// Uses a deliberately-small topN (2) and a fakeEmbedder that doesn't
// favour the matching tool — if keyword matching is working, the
// tool still gets selected.
func TestToolSelector_KeywordMatchOverridesSemanticRank(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(simpleToolDefinition{name: "start_task", desc: "Kick off an autonomous background research task when the user asks you to investigate or dig into something."})
	// Fillers whose descriptions are irrelevant to the query but will
	// score non-zero against any embedding.
	r.Register(simpleToolDefinition{name: "filler_1", desc: "Does filler thing one."})
	r.Register(simpleToolDefinition{name: "filler_2", desc: "Does filler thing two."})
	r.Register(simpleToolDefinition{name: "filler_3", desc: "Does filler thing three."})
	r.Register(simpleToolDefinition{name: "filler_4", desc: "Does filler thing four."})

	emb := &fakeEmbedder{dim: 8}
	sel := service.NewToolSelector(r, emb, service.WithToolSelectorTopN(2))

	specs, err := sel.Select(context.Background(), "could you kick off a research task to find me a microphone")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Function.Name
	}
	found := slices.Contains(names, "start_task")
	if !found {
		t.Errorf("keyword-match selection missed start_task; got tools: %v", names)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"zero a", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
		{"len mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.CosineSimilarity(tt.a, tt.b)
			if math.Abs(float64(got-tt.want)) > 1e-6 {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// directedEmbedder returns a vector strongly aligned with a given axis
// based on a keyword match — lets tests drive predictable ranking.
//
// Non-determinism note: when multiple axis keys match the same text,
// Go's map iteration order decides which axis fires. Tests using this
// fake must design queries + axes so at most one key matches per call,
// or accept tie-breaking in assertions.
type directedEmbedder struct {
	axis map[string]int // substring → which axis to fire on
	dim  int
}

func (d *directedEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, d.dim)
	for key, idx := range d.axis {
		if strings.Contains(strings.ToLower(text), key) {
			v[idx] = 1
			return v, nil
		}
	}
	return v, nil // zero vector = no axis match
}

func TestToolSelectorSelectRanksByRelevance(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(simpleToolDefinition{name: "weather", desc: "get current weather conditions"})
	r.Register(simpleToolDefinition{name: "timer", desc: "set a countdown timer"})
	r.Register(simpleToolDefinition{name: "remember", desc: "store a fact about the user"})
	r.Register(simpleToolDefinition{name: "wiki", desc: "search wikipedia articles"})

	emb := &directedEmbedder{
		dim: 8,
		axis: map[string]int{
			"weather":   0,
			"timer":     1,
			"fact":      2, // matches "store a fact"
			"wikipedia": 3,
		},
	}
	sel := service.NewToolSelector(r, emb,
		service.WithToolSelectorTopN(2),
		service.WithAlwaysOnTools("remember"),
	)

	specs, err := sel.Select(context.Background(), "what's the weather")
	if err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool, len(specs))
	for _, s := range specs {
		names[s.Function.Name] = true
	}

	// Always-on must be present.
	if !names["remember"] {
		t.Fatalf("always-on remember missing: %v", names)
	}
	// Top-ranked for "weather" must be present.
	if !names["weather"] {
		t.Fatalf("top-ranked weather missing: %v", names)
	}
	// TopN=2 plus 1 always-on = 3 total, de-duplicated if overlap.
	if len(specs) > 3 {
		t.Fatalf("expected ≤3 specs, got %d: %v", len(specs), names)
	}
}

func TestToolSelectorSelectDeduplicatesAlwaysOnFromTopN(t *testing.T) {
	r := tools.NewRegistry()
	r.Register(simpleToolDefinition{name: "weather", desc: "get current weather conditions"})

	emb := &directedEmbedder{dim: 8, axis: map[string]int{"weather": 0}}
	sel := service.NewToolSelector(r, emb,
		service.WithToolSelectorTopN(5),
		service.WithAlwaysOnTools("weather"), // same tool in both paths
	)

	specs, err := sel.Select(context.Background(), "weather please")
	if err != nil {
		t.Fatal(err)
	}

	if len(specs) != 1 {
		t.Fatalf("expected 1 spec (dedup), got %d", len(specs))
	}
}

func TestToolSelectorShrinksTokenBudget(t *testing.T) {
	r := tools.NewRegistry()
	// Register 50 stub tools with realistic-ish descriptions.
	for i := range 50 {
		r.Register(simpleToolDefinition{
			name: fmt.Sprintf("tool_%02d", i),
			desc: fmt.Sprintf("performs action %d on the system with various parameters and options", i),
		})
	}

	// Full registry ship — the baseline before dynamic selection.
	full := service.LLMToolSpecs(r, nil)
	fullCost := service.EstimateToolSpecsTokens(full)

	emb := &fakeEmbedder{dim: 32}
	sel := service.NewToolSelector(r, emb,
		service.WithToolSelectorTopN(15),
	)

	selected, err := sel.Select(context.Background(), "any query")
	if err != nil {
		t.Fatal(err)
	}

	selectedCost := service.EstimateToolSpecsTokens(selected)

	if len(selected) > 15 {
		t.Fatalf("selected %d tools, expected ≤15", len(selected))
	}
	// The selected set must cost meaningfully less than the full set.
	// Using half as a loose bound — with 15/50 ≈ 30% of tools kept, real
	// savings should be closer to 60-70% but we avoid brittle thresholds.
	if selectedCost >= fullCost/2 {
		t.Fatalf("selected cost %d not meaningfully below full cost %d", selectedCost, fullCost)
	}
	t.Logf("token budget: full=%d selected=%d reduction=%d%%",
		fullCost, selectedCost, 100-(selectedCost*100/fullCost))
}
