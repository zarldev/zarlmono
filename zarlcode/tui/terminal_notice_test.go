package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestHandleRunnerTerminalReasonNotice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason runner.TerminalReason
		want   string
	}{{"max", runner.TerminalMaxIterations, "iteration limit"}, {"cancelled", runner.TerminalCancelled, "cancelled"}, {"completed", runner.TerminalCompleted, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			m := tui.New()
			step(t, m, window(120, 32))
			step(t, m, teasink.ConversationStartedMsg{TaskID: tc.name, Prompt: "hi"})
			step(t, m, teasink.ConversationEndedMsg{TaskID: tc.name, Reason: tc.reason, Iterations: 3})
			out := m.View().Content
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Fatalf("missing %q:\n%s", tc.want, out)
			}
			if tc.want == "" && (strings.Contains(out, "iteration limit") || strings.Contains(out, "turn cancelled")) {
				t.Fatalf("normal completion has notice:\n%s", out)
			}
		})
	}
}
