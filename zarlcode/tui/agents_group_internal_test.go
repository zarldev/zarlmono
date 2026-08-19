package tui

import (
	"strings"
	"testing"
)

func TestTimelineGroupsSubAgentsUnderOneAgentsSection(t *testing.T) {
	tl := newTimeline()
	first := tl.startSubAgent("first", 1, "reviewer", "review changes")
	second := tl.startSubAgent("second", 1, "tester", "run tests")

	if len(tl.items) != 1 {
		t.Fatalf("top-level items = %d, want one agents section", len(tl.items))
	}
	agents, ok := tl.items[0].(*groupItem)
	if !ok || agents.kind != groupAgents {
		t.Fatalf("top-level item = %T, want agents group", tl.items[0])
	}
	if len(agents.children) != 2 || agents.children[0] != first || agents.children[1] != second {
		t.Fatalf("agents children = %#v, want both sub-agents in start order", agents.children)
	}

	collapsed := strings.Join(agents.render(100), "\n")
	if !strings.Contains(collapsed, "agents (2)") || strings.Contains(collapsed, "review changes") {
		t.Fatalf("collapsed agents section = %q", collapsed)
	}
	agents.toggle()
	expanded := strings.Join(agents.render(100), "\n")
	for _, want := range []string{"reviewer: review changes", "tester: run tests"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded agents section missing %q: %q", want, expanded)
		}
	}
}
