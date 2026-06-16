package compact_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// fakeStateProvider is the test double for StateProvider — lets us
// assert the executive briefing wires the structured sections from
// state into the rendered output verbatim.
type fakeStateProvider struct {
	plan  []compact.PlanStep
	files []compact.FileTouch
	tools []compact.ToolUsage
}

func (f *fakeStateProvider) Plan() []compact.PlanStep          { return f.plan }
func (f *fakeStateProvider) WorkingFiles() []compact.FileTouch { return f.files }
func (f *fakeStateProvider) TopTools() []compact.ToolUsage     { return f.tools }

// execFakeProvider produces a scripted narrative for the LLM call.
// Embeds llm.Provider so we inherit nil methods we don't need.
type execFakeProvider struct {
	llm.Provider
	body string
	err  error
}

func (f execFakeProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	if f.err != nil {
		return nil, f.err
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: f.body, Done: true}, nil)
	}, nil
}

func TestExecutive_FullBriefingShape(t *testing.T) {
	t.Parallel()
	state := &fakeStateProvider{
		plan: []compact.PlanStep{
			{Title: "scaffold", Status: "completed"},
			{Title: "wire handlers", Status: "in_progress", Note: "currently in shell.go"},
			{Title: "tests", Status: "pending"},
		},
		files: []compact.FileTouch{
			{Path: "/ws/pkg/foo.go", Action: "edit"},
			{Path: "/ws/cmd/main.go", Action: "read"},
		},
		tools: []compact.ToolUsage{
			{Name: "read", Count: 12},
			{Name: "grep", Count: 7},
			{Name: "bash", Count: 3},
		},
	}
	prov := execFakeProvider{body: "Synthesised narrative covering goal X and decision Y."}
	e := compact.NewExecutive(prov, "fake-model", state)
	history := []llm.Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "do thing 1"},
		{Role: "assistant", Content: "doing"},
		{Role: "user", Content: "do thing 2"},
		{Role: "assistant", Content: "doing"},
		{Role: "user", Content: "thing 3"},
		{Role: "assistant", Content: "done"},
	}
	res, err := e.Compact(context.Background(), history, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.Engine != "executive" {
		t.Errorf("engine = %q, want executive", res.Engine)
	}
	// system + 1 briefing + 2 verbatim recent = 4
	if len(res.History) != 4 {
		t.Fatalf("history len = %d, want 4", len(res.History))
	}
	briefing := res.History[1].Content
	for _, want := range []string{
		"[compacted — executive briefing",
		"PLAN PROGRESS",
		"scaffold",
		"WORKING FILES",
		"pkg/foo.go",
		"TOOL USAGE",
		"read × 12",
		"NARRATIVE",
		"Synthesised narrative",
	} {
		if !strings.Contains(briefing, want) {
			t.Errorf("briefing missing %q\n--- briefing ---\n%s", want, briefing)
		}
	}
	// Recent two messages stay verbatim.
	if res.History[2].Content != "thing 3" || res.History[3].Content != "done" {
		t.Errorf("recent slice = %v / %v", res.History[2].Content, res.History[3].Content)
	}
}

func TestExecutive_NoState_NarrativeOnly(t *testing.T) {
	t.Parallel()
	prov := execFakeProvider{body: "summary text"}
	e := compact.NewExecutive(prov, "", nil)
	history := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "more"},
		{Role: "assistant", Content: "done"},
	}
	res, err := e.Compact(context.Background(), history, 1)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	briefing := res.History[0].Content
	if strings.Contains(briefing, "PLAN PROGRESS") {
		t.Errorf("expected no PLAN section without state, got:\n%s", briefing)
	}
	if !strings.Contains(briefing, "NARRATIVE") {
		t.Errorf("expected NARRATIVE section, got:\n%s", briefing)
	}
}

func TestExecutive_HistoryShorterThanKeep(t *testing.T) {
	t.Parallel()
	prov := execFakeProvider{body: "should not be called"}
	e := compact.NewExecutive(prov, "", &fakeStateProvider{})
	history := []llm.Message{
		{Role: "user", Content: "only one"},
	}
	res, err := e.Compact(context.Background(), history, 8)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Short history → unchanged.
	if len(res.History) != 1 || res.History[0].Content != "only one" {
		t.Errorf("expected passthrough, got %v", res.History)
	}
}

func TestExecutive_ProviderFailure(t *testing.T) {
	t.Parallel()
	prov := execFakeProvider{err: errors.New("rate-limited")}
	e := compact.NewExecutive(prov, "", &fakeStateProvider{})
	history := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
		{Role: "assistant", Content: "d"},
	}
	_, err := e.Compact(context.Background(), history, 1)
	if err == nil {
		t.Error("expected error from failing provider")
	}
}

func TestExecutive_NilProvider(t *testing.T) {
	t.Parallel()
	e := &compact.Executive{}
	_, err := e.Compact(context.Background(), []llm.Message{{Role: "user", Content: "x"}}, 0)
	if err == nil {
		t.Error("expected error for nil provider")
	}
}

func TestExecutive_TopToolsTrimmedToFive(t *testing.T) {
	t.Parallel()
	state := &fakeStateProvider{
		tools: []compact.ToolUsage{
			{Name: "a", Count: 1},
			{Name: "b", Count: 9},
			{Name: "c", Count: 5},
			{Name: "d", Count: 7},
			{Name: "e", Count: 3},
			{Name: "f", Count: 4},
			{Name: "g", Count: 8},
		},
	}
	prov := execFakeProvider{body: "n"}
	e := compact.NewExecutive(prov, "", state)
	history := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
		{Role: "assistant", Content: "d"},
	}
	res, _ := e.Compact(context.Background(), history, 1)
	briefing := res.History[0].Content
	// Top 5 by count desc: b(9), g(8), d(7), c(5), f(4) — a/e shouldn't appear.
	for _, want := range []string{"b × 9", "g × 8", "d × 7"} {
		if !strings.Contains(briefing, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(briefing, "a × 1") || strings.Contains(briefing, "e × 3") {
		t.Errorf("expected only top 5 tools, found lower-ranked ones\n%s", briefing)
	}
}
