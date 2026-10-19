package teasink_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestSinkToolOutputCoalescesLatestSnapshot(t *testing.T) {
	send, messages := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)

	event := toolOutputEvent("task", 0, "execution", "first")
	sink.OnToolOutput(t.Context(), event)
	event.Text = "second"
	sink.OnToolOutput(t.Context(), event)
	sink.Drain()

	got := toolOutputMessages(messages())
	if len(got) != 1 {
		t.Fatalf("tool output messages = %d, want 1", len(got))
	}
	if got[0].Text != "second" {
		t.Fatalf("text = %q, want latest snapshot %q", got[0].Text, "second")
	}
}

func TestSinkToolOutputIsolationAndExecutionIDReuse(t *testing.T) {
	send, messages := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)

	sink.OnToolOutput(t.Context(), toolOutputEvent("task-a", 0, "shared", "a"))
	sink.OnToolOutput(t.Context(), toolOutputEvent("task-b", 0, "shared", "b"))
	sink.OnToolOutput(t.Context(), toolOutputEvent("task-a", 1, "shared", "nested"))
	sink.OnToolOutput(t.Context(), toolOutputEvent("task-a", 0, "", "legacy-one"))
	legacyTwo := toolOutputEvent("task-a", 0, "", "legacy-two")
	legacyTwo.ToolID = "other-tool"
	legacyTwo.Sequence = 2
	sink.OnToolOutput(t.Context(), legacyTwo)
	sink.Drain()

	got := toolOutputMessages(messages())
	if len(got) != 5 {
		t.Fatalf("isolated tool output messages = %d, want 5", len(got))
	}

	sink.OnToolCompleted(t.Context(), runner.ToolCompleted{
		TaskID: taskscope.ID("task-a"), Depth: 0, ExecutionID: "shared", ToolID: "tool", ToolName: "bash",
	})
	sink.OnToolStarted(t.Context(), runner.ToolStarted{
		TaskID: taskscope.ID("task-a"), Depth: 0, ExecutionID: "shared", ToolID: "tool", ToolName: "bash",
	})
	sink.OnToolOutput(t.Context(), toolOutputEvent("task-a", 0, "shared", "reused"))
	sink.Drain()

	got = toolOutputMessages(messages())
	if len(got) != 6 || got[5].Text != "reused" {
		t.Fatalf("reused execution outputs = %#v, want final reused snapshot", got)
	}
}

func TestSinkToolOutputPrecedesTerminalAndLateOutputIsSealed(t *testing.T) {
	send, messages := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)

	event := toolOutputEvent("task", 0, "execution", "before terminal")
	sink.OnToolOutput(t.Context(), event)
	sink.OnToolCompleted(t.Context(), runner.ToolCompleted{
		TaskID: event.TaskID, Depth: event.Depth, ExecutionID: event.ExecutionID,
		ToolID: event.ToolID, ToolName: event.ToolName,
	})
	event.Text = "late"
	sink.OnToolOutput(t.Context(), event)
	sink.Drain()

	got := messages()
	if len(got) != 2 {
		t.Fatalf("messages = %d, want output and terminal", len(got))
	}
	if output, ok := got[0].(teasink.ToolOutputMsg); !ok || output.Text != "before terminal" {
		t.Fatalf("first message = %#v, want tool output", got[0])
	}
	if _, ok := got[1].(teasink.ToolCompletedMsg); !ok {
		t.Fatalf("second message = %T, want ToolCompletedMsg", got[1])
	}
}

func TestSinkToolOutputBoundsPendingAndUTF8Bytes(t *testing.T) {
	send, messages := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)

	oversized := strings.Repeat("x", tools.MaxToolOutputPreviewBytes-1) + "€" + string([]byte{0xff})
	sink.OnToolOutput(t.Context(), toolOutputEvent("large", 0, "large", oversized))
	for index := range 200 {
		event := toolOutputEvent("task", 0, string(rune(index+1)), "snapshot")
		sink.OnToolOutput(t.Context(), event)
	}
	sink.Drain()

	got := toolOutputMessages(messages())
	if len(got) > 128 {
		t.Fatalf("pending tool output messages = %d, want at most 128", len(got))
	}
	if len(got) == 0 {
		t.Fatal("expected bounded tool output messages")
	}
	if len(got[0].Text) > tools.MaxToolOutputPreviewBytes {
		t.Fatalf("preview bytes = %d, want at most %d", len(got[0].Text), tools.MaxToolOutputPreviewBytes)
	}
	if !utf8.ValidString(got[0].Text) {
		t.Fatal("preview is not valid UTF-8")
	}
}

func TestSinkToolOutputCancellationCloseAndNilSend(t *testing.T) {
	sink := teasink.New(nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sink.OnToolOutput(ctx, toolOutputEvent("task", 0, "execution", "cancelled context"))
	sink.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			sink.OnToolOutput(ctx, toolOutputEvent("task", 0, "execution", "late"))
		}
	}()
	<-done
}

func toolOutputEvent(taskID string, depth int, executionID, text string) runner.ToolOutputProgress {
	return runner.ToolOutputProgress{
		TaskID: taskscope.ID(taskID), Depth: depth, ExecutionID: executionID,
		ToolID: "tool", ToolName: "bash", Sequence: 1, Text: text,
	}
}

func toolOutputMessages(messages []tea.Msg) []teasink.ToolOutputMsg {
	outputs := make([]teasink.ToolOutputMsg, 0, len(messages))
	for _, message := range messages {
		if output, ok := message.(teasink.ToolOutputMsg); ok {
			outputs = append(outputs, output)
		}
	}
	return outputs
}
