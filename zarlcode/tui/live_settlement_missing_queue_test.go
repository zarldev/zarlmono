package tui_test

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestLiveTurnMissingTerminalPreservesQueuedProjection(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	// An embedder without persistence uses canonical queued-user entries,
	// unlike the temporary queue intents used by durable dispatch.
	f.ui = tui.New()
	f.ui.SetLiveRunner(f.live)
	f.ui.SetLiveEventSink(f.sink)
	f.ui.SetStartupReady(true)
	driveBeforeCommand(f, f.ui.Submit("first"))
	f.sink.Drain()
	f.mu.Lock()
	events := append([]tea.Msg(nil), f.events...)
	f.events = nil
	f.mu.Unlock()
	if len(events) < 2 {
		t.Fatal("no turn events")
	}
	var taskID string
	for _, event := range events[:len(events)-1] {
		if terminal, ok := event.(teasink.ConversationEndedMsg); ok {
			taskID = terminal.TaskID
			continue
		}
		f.ui.Update(event)
	}
	// Model missing child/tool terminal observations as well as the root's.
	f.ui.Update(teasink.ConversationStartedMsg{TaskID: "unfinished-child", Depth: 1, AgentName: "child"})
	f.ui.Update(teasink.ToolStartedMsg{TaskID: taskID, ExecutionID: "unfinished-tool", ToolID: "unfinished-tool", ToolName: "inspect"})
	texts := []string{"queued first", "queued second"}
	for _, text := range texts {
		f.ui.Submit(text)
	}
	f.ui.Update(events[len(events)-1])
	queued := make(map[string]string)
	tools, children := 0, 0
	for _, entry := range f.ui.CanonicalThread().Entries() {
		switch entry.Kind {
		case transcript.EntryKinds.ENTRYQUEUEDUSER:
			queued[entry.Payload.Text] = entry.ID
		case transcript.EntryKinds.ENTRYTOOLCALL:
			tools++
			if entry.Payload.ToolState != transcript.ToolInterrupted {
				t.Fatal("completed runtime retained a running tool projection")
			}
		case transcript.EntryKinds.ENTRYSUBAGENT:
			children++
			if entry.Payload.Subagent != transcript.SubagentInterrupted {
				t.Fatal("completed runtime retained a running child projection")
			}
		}
	}
	if len(queued) != len(texts) || f.live.QueueLen() != len(texts) || tools != 1 || children != 1 {
		t.Fatal("recovery lost queued input or unfinished lifecycle projections")
	}
	admitted := make(map[string]bool)
	for _, text := range texts {
		message, ok := f.live.PopQueuedInput()
		if !ok || message.Content != text {
			t.Fatal("recovery changed the explicit admission order")
		}
		f.ui.Update(teasink.SteerInjectedMsg{TaskID: taskID, Messages: []llm.Message{message}})
		admitted[text] = true
		count := 0
		for _, entry := range f.ui.CanonicalThread().Entries() {
			if _, pending := queued[entry.Payload.Text]; !pending {
				continue
			}
			count++
			if entry.ID != queued[entry.Payload.Text] || entry.Payload.Injected != admitted[entry.Payload.Text] {
				t.Fatal("admission duplicated or incorrectly marked pending queue entries")
			}
		}
		if count != len(texts) {
			t.Fatalf("queued entries after admission = %d, want %d", count, len(texts))
		}
	}
}
