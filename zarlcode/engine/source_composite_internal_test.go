package engine

import (
	"context"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestCompositeSource_SeesSecondaryRegistrationsAfterConstruction(t *testing.T) {
	primary := tools.NewRegistry()
	primary.Register(fakeTool{name: code.ToolNameRead})
	secondary := tools.NewRegistry()
	src := newCompositeSource(primary, secondary)

	if sourceHasRemoteTool(t.Context(), src) {
		t.Fatal("remote_tool should not be visible before registration")
	}

	secondary.Register(fakeTool{name: "remote_tool"})
	if !sourceHasRemoteTool(t.Context(), src) {
		t.Fatal("remote_tool should become visible without rebuilding the source")
	}

	if _, err := src.Execute(t.Context(), tools.ToolCall{ToolName: "remote_tool"}); err != nil {
		t.Fatalf("execute remote_tool: %v", err)
	}
}

func TestCompositeSource_PrimaryWinsOnDuplicateNames(t *testing.T) {
	primary := tools.NewRegistry()
	primary.Register(fakeTool{name: code.ToolNameRead})
	secondary := tools.NewRegistry()
	secondary.Register(fakeTool{name: code.ToolNameRead})

	src := newCompositeSource(primary, secondary)
	if got := countListedTool(t.Context(), src, code.ToolNameRead); got != 1 {
		t.Fatalf("listed duplicate count = %d, want 1", got)
	}
}

func countListedTool(ctx context.Context, src tools.Source, name tools.ToolName) int {
	count := 0
	for t := range src.Tools(ctx) {
		if t.Definition().Name == name {
			count++
		}
	}
	return count
}

// countingSource is a versioned tool source that records how many times its
// tools were enumerated, so tests can assert when the composite re-walks it.
type countingSource struct {
	names []tools.ToolName
	ver   int
	walks int
}

func (s *countingSource) Version() int { return s.ver }

func (s *countingSource) Tools(context.Context) iter.Seq[tools.Tool] {
	s.walks++
	return func(yield func(tools.Tool) bool) {
		for _, n := range s.names {
			if !yield(fakeTool{name: n}) {
				return
			}
		}
	}
}

func (s *countingSource) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{}, nil
}

func TestCompositeSource_CachesWhileVersionsStable(t *testing.T) {
	primary := &countingSource{names: []tools.ToolName{code.ToolNameRead}}
	secondary := &countingSource{names: []tools.ToolName{"remote_tool"}}
	src := newCompositeSource(primary, secondary)

	for i := range 3 {
		if got := countListedTool(t.Context(), src, "remote_tool"); got != 1 {
			t.Fatalf("listing %d: remote_tool count = %d, want 1", i, got)
		}
		if _, err := src.Execute(t.Context(), tools.ToolCall{ToolName: code.ToolNameRead}); err != nil {
			t.Fatalf("execute read: %v", err)
		}
	}

	// One rebuild populates the cache; the remaining listings + executes reuse
	// it, so each operand is walked exactly once despite many calls.
	if primary.walks != 1 || secondary.walks != 1 {
		t.Fatalf("operand walks = (primary %d, secondary %d), want (1, 1)", primary.walks, secondary.walks)
	}
}

func TestCompositeSource_RebuildsOnVersionBump(t *testing.T) {
	primary := &countingSource{names: []tools.ToolName{code.ToolNameRead}}
	secondary := &countingSource{ver: 1}
	src := newCompositeSource(primary, secondary)

	if sourceHasRemoteTool(t.Context(), src) {
		t.Fatal("remote_tool should not be visible before registration")
	}

	// Simulate a registration into the secondary: add the tool and bump.
	secondary.names = append(secondary.names, "remote_tool")
	secondary.ver++
	if !sourceHasRemoteTool(t.Context(), src) {
		t.Fatal("remote_tool should become visible after the version bump")
	}
	if secondary.walks < 2 {
		t.Fatalf("secondary walked %d times, want >= 2 (cache should have invalidated)", secondary.walks)
	}
}

// sourceHasRemoteTool reports whether src exposes the "remote_tool" fixture.
// Test-only helper for the composite-source tests.
func sourceHasRemoteTool(ctx context.Context, src tools.Iterable) bool {
	if src == nil {
		return false
	}
	for t := range src.Tools(ctx) {
		if t.Definition().Name == "remote_tool" {
			return true
		}
	}
	return false
}
