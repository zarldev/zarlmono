package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestSidebar_ShowsRunState(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "do the thing"},
		teasink.ToolStartedMsg{TaskID: "t1", ToolID: "c1", ToolName: "bash"},
		teasink.ToolCompletedMsg{TaskID: "t1", ToolID: "c1", ToolName: "bash", Duration: time.Second},
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1, Usage: &llm.Usage{PromptTokens: 120, TotalTokens: 200}},
	)
	// The run state shows on the title ("⠋ running"); the body shows the
	// CONTEXT section, the tools histogram, and the live context occupancy
	// (prompt tokens) against the window — "120" is the gauge numerator, not
	// the total. (Iteration/tool counters were folded off the panel into the
	// title's status + live tok/s.)
	for _, want := range []string{"[state]", "running", "[tools]", "[context]", "120"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q:\n%s", want, out)
		}
	}
}

func TestSidebar_SectionRulesMeetFrame(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "do the thing"},
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1, Usage: &llm.Usage{PromptTokens: 120, TotalTokens: 200}},
	)

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "[context]") {
			continue
		}
		start := strings.LastIndex(line, "├─[context]")
		if start < 0 {
			t.Fatalf("context section rule should start with a frame joint, got %q", line)
		}
		section := line[start:]
		if !strings.HasSuffix(section, "┤") {
			t.Fatalf("context section rule should meet both frame edges, got %q", line)
		}
		if strings.Contains(line, "│ ├─[context]") || strings.Contains(line, "[context] ") {
			t.Fatalf("context section rule should not keep sidebar gutter spaces, got %q", line)
		}
		return
	}
	t.Fatalf("context section rule missing:\n%s", out)
}

func TestSidebar_RunCompletesToIdle(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "go"},
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1},
		teasink.ConversationEndedMsg{TaskID: "t1", Iterations: 1},
	)
	if !strings.Contains(out, "idle") {
		t.Errorf("completed run should read idle:\n%s", out)
	}
}

func TestSidebar_OnlyLastTurnReportsTokPerSec(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "go"},
		// A usage-bearing iteration: the runner sets occupancy (Usage) and
		// this iteration's own usage (Delta) to the same values.
		teasink.IterationCompletedMsg{TaskID: "t1", Iter: 1,
			Usage: &llm.Usage{
				PromptTokens:     100,
				CompletionTokens: 120,
				TotalTokens:      220,
			},
			Delta: &llm.Usage{
				PromptTokens:     100,
				CompletionTokens: 120,
				TotalTokens:      220,
			},
		},
		teasink.ConversationEndedMsg{
			TaskID: "t1", Iterations: 1, Duration: 2 * time.Second,
			TotalUsage: &llm.Usage{PromptTokens: 100, CompletionTokens: 120, TotalTokens: 220},
		},
	)

	if got := strings.Count(out, "tok/s"); got != 1 {
		t.Fatalf("tok/s should be reported once in the cockpit last-turn row, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "60 tok/s") {
		t.Errorf("last-turn provider-token throughput missing:\n%s", out)
	}
}
