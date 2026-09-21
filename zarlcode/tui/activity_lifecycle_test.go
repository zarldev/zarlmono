package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type activityProvider struct{}

func (activityProvider) Name() string { return "activity-test" }
func (activityProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "done"}, nil)
	}
}

func TestActivityRunCommandClearsWithoutTerminalEvent(t *testing.T) {
	for _, setupFailure := range []bool{false, true} {
		name := "completion safety net"
		if setupFailure {
			name = "setup failure"
		}
		t.Run(name, func(t *testing.T) {
			// This test needs no host sleep inhibitor; RunFn treats its absence as
			// optional. Keep the real command adapter and runner lifecycle.
			t.Setenv("PATH", t.TempDir())
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := ws.Close(); err != nil {
					t.Error(err)
				}
			})
			live := engine.NewLiveRunner(activityProvider{}, ws, "test-model")
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
				defer cancel()
				if err := live.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			if setupFailure {
				if err := live.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			ui := newActivityUI()
			ui.Update(teasink.ToolStartedMsg{TaskID: "turn", ToolID: "read", ToolName: "read"})
			assertLiveActivity(t, ui, "running read")
			// The runner uses its default no-op sink: only the returned completion
			// or setup-failure message reaches the UI, not ConversationEnded.
			ui.Update(tui.RunFn(t.Context(), live, "work")())
			out := ansi.Strip(ui.View().Content)
			if !strings.Contains(out, "○ idle") || strings.Contains(out, "⠋ ") {
				t.Fatalf("command completion left live activity:\n%s", out)
			}
			if strings.Contains(ui.ToastText(), "setup:") != setupFailure {
				t.Fatalf("setup-failure toast = %q, want failure=%v", ui.ToastText(), setupFailure)
			}
			ui.Update(teasink.ConversationStartedMsg{TaskID: "next"})
			assertLiveActivity(t, ui, "working")
		})
	}
}
