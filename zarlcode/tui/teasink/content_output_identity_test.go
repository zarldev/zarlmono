package teasink_test

import (
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestSinkContentOutputIdentity(t *testing.T) {
	t.Run("same item including zero merges", func(t *testing.T) {
		messages := deliverContent(t,
			runner.Content{TaskID: "task", Delta: "first", ContentOutputIndex: outputIndex(0)},
			runner.Content{TaskID: "task", Delta: " second", ContentOutputIndex: outputIndex(0)},
		)

		assertContentMessages(t, messages, []expectedContent{{taskID: "task", delta: "first second", outputIndex: outputIndex(0)}})
	})

	t.Run("different known items flush in exact order", func(t *testing.T) {
		messages := deliverContent(t,
			runner.Content{TaskID: "task", Delta: "first", ContentOutputIndex: outputIndex(0)},
			runner.Content{TaskID: "task", Delta: "second", ContentOutputIndex: outputIndex(1)},
		)

		assertContentMessages(t, messages, []expectedContent{
			{taskID: "task", delta: "first", outputIndex: outputIndex(0)},
			{taskID: "task", delta: "second", outputIndex: outputIndex(1)},
		})
	})

	t.Run("absent identity does not split unnecessarily", func(t *testing.T) {
		messages := deliverContent(t,
			runner.Content{TaskID: "task", Delta: "known", ContentOutputIndex: outputIndex(2)},
			runner.Content{TaskID: "task", Delta: " unknown"},
			runner.Content{TaskID: "task", Delta: " known", ContentOutputIndex: outputIndex(2)},
		)

		assertContentMessages(t, messages, []expectedContent{{taskID: "task", delta: "known unknown known", outputIndex: outputIndex(2)}})
	})

	t.Run("all absent remains absent", func(t *testing.T) {
		messages := deliverContent(t,
			runner.Content{TaskID: "task", Delta: "first"},
			runner.Content{TaskID: "task", Delta: " second"},
		)

		assertContentMessages(t, messages, []expectedContent{{taskID: "task", delta: "first second"}})
	})
	t.Run("leading unknown is not relabelled as a later item", func(t *testing.T) {
		messages := deliverContent(t, runner.Content{TaskID: "task", Delta: "unknown"}, runner.Content{TaskID: "task", Delta: "known", ContentOutputIndex: outputIndex(0)})
		assertContentMessages(t, messages, []expectedContent{{taskID: "task", delta: "unknown"}, {taskID: "task", delta: "known", outputIndex: outputIndex(0)}})
	})
}

func TestSinkContentOutputIdentityOwnershipAndIsolation(t *testing.T) {
	send, snapshot := identityRecordingSend()
	sink := teasink.New(send)
	defer sink.Close()

	firstIndex, secondIndex, depthIndex := 0, 0, 1
	sink.OnContent(t.Context(), runner.Content{TaskID: "first", Delta: "a", ContentOutputIndex: &firstIndex})
	sink.OnContent(t.Context(), runner.Content{TaskID: "second", Delta: "b", ContentOutputIndex: &secondIndex})
	sink.OnContent(t.Context(), runner.Content{TaskID: "first", Depth: 1, Delta: "c", ContentOutputIndex: &depthIndex})
	firstIndex, secondIndex, depthIndex = 7, 8, 9
	sink.Drain()

	messages := snapshot()
	assertContentMessages(t, messages, []expectedContent{
		{taskID: "first", delta: "a", outputIndex: outputIndex(0)},
		{taskID: "second", delta: "b", outputIndex: outputIndex(0)},
		{taskID: "first", depth: 1, delta: "c", outputIndex: outputIndex(1)},
	})

	first := messages[0].(teasink.ContentMsg)
	second := messages[1].(teasink.ContentMsg)
	*first.ContentOutputIndex = 42
	if *second.ContentOutputIndex != 0 {
		t.Fatalf("message identities alias: second = %d, want 0", *second.ContentOutputIndex)
	}
}

func TestSinkIterationFlushPreservesContentOrder(t *testing.T) {
	send, snapshot := identityRecordingSend()
	sink := teasink.New(send)
	defer sink.Close()

	sink.OnContent(t.Context(), runner.Content{TaskID: "task", Delta: "before", ContentOutputIndex: outputIndex(0)})
	sink.OnIterationCompleted(t.Context(), runner.IterationCompleted{TaskID: "task", Iter: 0})
	sink.OnContent(t.Context(), runner.Content{TaskID: "task", Delta: "after", ContentOutputIndex: outputIndex(1)})
	sink.Drain()

	messages := snapshot()
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	assertContentMessages(t, []tea.Msg{messages[0]}, []expectedContent{{taskID: "task", delta: "before", outputIndex: outputIndex(0)}})
	if completed, ok := messages[1].(teasink.IterationCompletedMsg); !ok || completed.TaskID != "task" || completed.Iter != 0 {
		t.Fatalf("message[1] = %#v, want iteration completion", messages[1])
	}
	assertContentMessages(t, []tea.Msg{messages[2]}, []expectedContent{{taskID: "task", delta: "after", outputIndex: outputIndex(1)}})
}

type expectedContent struct {
	taskID      string
	depth       int
	delta       string
	outputIndex *int
}

func deliverContent(t *testing.T, contents ...runner.Content) []tea.Msg {
	t.Helper()
	var messages []tea.Msg
	synctest.Test(t, func(t *testing.T) {
		send, snapshot := identityRecordingSend()
		sink := teasink.New(send)
		defer sink.Close()
		for _, content := range contents {
			sink.OnContent(t.Context(), content)
		}
		sink.Drain()
		messages = snapshot()
	})
	return messages
}

func identityRecordingSend() (func(tea.Msg), func() []tea.Msg) {
	var messages []tea.Msg
	return func(message tea.Msg) {
			messages = append(messages, message)
		}, func() []tea.Msg {
			return append([]tea.Msg(nil), messages...)
		}
}

func assertContentMessages(t *testing.T, messages []tea.Msg, want []expectedContent) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("messages = %d, want %d", len(messages), len(want))
	}
	for i, expected := range want {
		message, ok := messages[i].(teasink.ContentMsg)
		if !ok {
			t.Fatalf("message[%d] = %T, want ContentMsg", i, messages[i])
		}
		if message.TaskID != expected.taskID || message.Depth != expected.depth || message.Delta != expected.delta {
			t.Errorf("message[%d] = (%q, %d, %q), want (%q, %d, %q)", i, message.TaskID, message.Depth, message.Delta, expected.taskID, expected.depth, expected.delta)
		}
		if !sameOutputIndex(message.ContentOutputIndex, expected.outputIndex) {
			t.Errorf("message[%d].ContentOutputIndex = %v, want %v", i, message.ContentOutputIndex, expected.outputIndex)
		}
	}
}

func outputIndex(value int) *int { return &value }

func sameOutputIndex(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
