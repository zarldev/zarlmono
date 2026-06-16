package taskrunner

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// fakeNotifier (capturing Push) is declared in zkit_sink_test.go — reused here.

type fakeProgress struct {
	calls       int
	lastSummary string
}

func (f *fakeProgress) UpdateProgress(_ context.Context, _ repository.TaskID, _ int, summary string) error {
	f.calls++
	f.lastSummary = summary
	return nil
}

func TestRunState(t *testing.T) {
	t.Parallel()
	st := &runState{}
	st.addFinding("a")
	st.markComplete("final")
	snap := st.snapshot()
	if len(snap.findings) != 2 || snap.findings[0] != "a" || snap.findings[1] != "final" {
		t.Fatalf("findings = %v, want [a final]", snap.findings)
	}
	if !snap.completed || snap.summary != "final" {
		t.Fatalf("completed=%v summary=%q, want true/final", snap.completed, snap.summary)
	}
	// snapshot is a copy — later mutation doesn't leak in.
	st.addFinding("b")
	if len(snap.findings) != 2 {
		t.Fatalf("snapshot mutated after the fact: %v", snap.findings)
	}
}

func TestDecideFinal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason            runner.TerminalReason
		completed, paused bool
		want              finalAction
	}{
		{runner.TerminalError, false, false, finalFailed},
		{runner.TerminalError, true, true, finalFailed}, // error wins
		{runner.TerminalCancelled, false, false, finalRequeue},
		{runner.TerminalCompleted, true, false, finalComplete},
		{runner.TerminalMaxIterations, false, false, finalComplete},
		{runner.TerminalCompleted, false, true, finalPaused},  // paused, not completed
		{runner.TerminalCompleted, true, true, finalComplete}, // completed wins over paused
	}
	for _, tt := range tests {
		if got := decideFinal(tt.reason, tt.completed, tt.paused); got != tt.want {
			t.Errorf("decideFinal(%v, completed=%v, paused=%v) = %d, want %d",
				tt.reason, tt.completed, tt.paused, got, tt.want)
		}
	}
}

func TestZkitCompleteTool_recordsAndRequiresSummary(t *testing.T) {
	t.Parallel()
	st := &runState{}
	tool := &zkitCompleteTool{state: st}

	res, _ := tool.Execute(context.Background(), tools.ToolCall{ID: "c1", Arguments: tools.ToolParameters{"summary": "done"}})
	if !res.Success {
		t.Fatalf("complete_task failed: %+v", res)
	}
	if snap := st.snapshot(); !snap.completed || snap.summary != "done" {
		t.Fatalf("state not marked complete: %+v", snap)
	}

	st2 := &runState{}
	res2, _ := (&zkitCompleteTool{state: st2}).Execute(context.Background(), tools.ToolCall{ID: "c2", Arguments: tools.ToolParameters{}})
	if res2.Success {
		t.Fatal("empty summary should fail validation")
	}
	if st2.snapshot().completed {
		t.Fatal("empty summary must not mark complete")
	}
}

func TestZkitReportTool_recordsAndFiresEffects(t *testing.T) {
	t.Parallel()
	st := &runState{}
	notif := &fakeNotifier{}
	prog := &fakeProgress{}
	emitted := ""
	tool := &zkitReportTool{
		state:    st,
		notify:   notif,
		progress: prog,
		emit:     func(f string) { emitted = f },
		task:     repository.Task{ID: "task1234abcdef", SessionID: "s1", PersonName: "p"},
	}

	res, _ := tool.Execute(context.Background(), tools.ToolCall{ID: "c1", Arguments: tools.ToolParameters{"finding": "found X"}})
	if !res.Success {
		t.Fatalf("report_progress failed: %+v", res)
	}
	if got := st.snapshot().findings; len(got) != 1 || got[0] != "found X" {
		t.Fatalf("findings = %v, want [found X]", got)
	}
	if len(notif.pushed) != 1 || !strings.Contains(notif.pushed[0].Content, "found X") {
		t.Fatalf("notification = %+v, want one containing 'found X'", notif.pushed)
	}
	if prog.calls != 1 || prog.lastSummary != "found X" {
		t.Fatalf("UpdateProgress calls=%d last=%q, want 1/found X", prog.calls, prog.lastSummary)
	}
	if emitted != "found X" {
		t.Fatalf("emit = %q, want found X", emitted)
	}

	// Empty finding fails and fires nothing more.
	res2, _ := tool.Execute(context.Background(), tools.ToolCall{ID: "c2", Arguments: tools.ToolParameters{}})
	if res2.Success {
		t.Fatal("empty finding should fail validation")
	}
	if len(notif.pushed) != 1 || prog.calls != 1 {
		t.Fatal("empty finding must not fire side effects")
	}
}

func TestZkitPauseTool_recordsAndNotifies(t *testing.T) {
	t.Parallel()
	st := &runState{}
	notif := &fakeNotifier{}
	tool := &zkitPauseTool{state: st, notify: notif, task: repository.Task{ID: "task1234abcdef", SessionID: "s1"}}

	res, _ := tool.Execute(context.Background(), tools.ToolCall{ID: "c1", Arguments: tools.ToolParameters{"reason": "blocked"}})
	if !res.Success {
		t.Fatalf("pause_task failed: %+v", res)
	}
	if snap := st.snapshot(); !snap.paused || snap.reason != "blocked" {
		t.Fatalf("state not paused: %+v", snap)
	}
	if len(notif.pushed) != 1 || !strings.Contains(notif.pushed[0].Content, "paused") {
		t.Fatalf("notification = %+v, want one mentioning paused", notif.pushed)
	}
}

func TestShortID(t *testing.T) {
	t.Parallel()
	if got := shortID("abcdefghij"); got != "abcdefgh" {
		t.Errorf("shortID long = %q, want abcdefgh", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID short = %q, want abc (no panic)", got)
	}
}
