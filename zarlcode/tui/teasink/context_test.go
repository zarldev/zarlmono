package teasink_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestSinkCancellationStillDeliversBufferedContentAndTerminalEvent(t *testing.T) {
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	defer sink.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink.OnContent(ctx, runner.Content{TaskID: "turn", Delta: "partial"})
	cancel()
	sink.OnConversationEnded(ctx, runner.ConversationEnded{TaskID: "turn", Reason: runner.TerminalCancelled})
	sink.Drain()
	messages := snapshot()
	if len(messages) != 2 {
		t.Fatalf("messages = %v; want content and terminal event", messages)
	}
	content, ok := messages[0].(teasink.ContentMsg)
	if !ok || content.TaskID != "turn" || content.Delta != "partial" {
		t.Fatalf("content = %#v", messages[0])
	}
	ended, ok := messages[1].(teasink.ConversationEndedMsg)
	if !ok || ended.TaskID != "turn" || ended.Reason != runner.TerminalCancelled {
		t.Fatalf("terminal = %#v", messages[1])
	}
}
